package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/decred/lightning-faucet/internal/static"
	"github.com/gorilla/mux"
	"golang.org/x/crypto/acme/autocert"
)

// equal reports whether the first argument is equal to any of the remaining
// arguments. This function is used as a custom function within templates to do
// richer equality tests.
func equal(x, y interface{}) bool {
	return reflect.DeepEqual(x, y)
}

var (
	// ctxb is a global context with no timeouts that's used within the
	// gRPC requests to lnd.
	ctxb = context.Background()
)

func main() {
	// Load configuration and parse command line.  This function also
	// initializes logging and configures it accordingly.
	cfg, _, err := loadConfig()
	if err != nil {
		return
	}

	// Pre-compile the list of templates so we'll catch any errors in the
	// templates as soon as the binary is run. If an directory is specified
	// in config, then we use the local files to generate templates.
	var faucetTemplates *template.Template

	if cfg.TemplatesDir != "" {
		faucetTemplates = template.Must(template.New("faucet").
			Funcs(template.FuncMap{
				"equal": equal,
			}).
			ParseGlob(filepath.Join("templates", "*.html")))

	} else {
		faucetTemplates = template.Must(template.New("faucet").Parse(""))
		for filepath, content := range static.Templates() {
			_, err := faucetTemplates.New(filepath[1:]).
				Parse(string(content))
			if err != nil {
				log.Criticalf("unable to using static blob: %v", err)
				return
			}
		}
	}

	// With the templates loaded, create the faucet itself.
	faucet, err := newLightningFaucet(cfg, faucetTemplates)
	if err != nil {
		log.Criticalf("unable to create faucet: %v", err)
		os.Exit(1)
		return
	}

	// If the wipe channels bool is set, then we'll attempt to close ALL
	// the faucet's channels by any means and exit in the case of a success
	// or failure.
	if cfg.WipeChannels {
		log.Info("Attempting to wipe all faucet channels")
		if err := faucet.CloseAllChannels(); err != nil {
			log.Criticalf("unable to close all the faucet's channels: %v", err)
			os.Exit(1)
			return
		}

		return
	}

	// If we're not wiping all the channels, then we'll launch the set of
	// goroutines required for the faucet to function.
	faucet.Start(cfg)

	// Create a new mux in order to route a request based on its path to a
	// dedicated http.Handler.
	r := mux.NewRouter()
	r.HandleFunc("/", faucet.faucetHome).Methods("POST", "GET")
	r.HandleFunc("/info", faucet.infoPage).Methods("GET")

	// If users disable all actions, then disable the route
	if !(cfg.DisableGenerateInvoices && cfg.DisablePayInvoices) {
		r.HandleFunc("/tools", faucet.toolsPage).Methods("POST", "GET")
	}

	// Next create a static file server which will dispatch our static
	// files load in the static pkg. If a directory is specified in config,
	// then we use local files to serve statics.
	if cfg.StaticDir != "" {
		staticFileServer := http.FileServer(http.Dir(cfg.StaticDir))
		staticHandler := http.StripPrefix("/static/", staticFileServer)
		r.PathPrefix("/static/").Handler(staticHandler)
	} else {
		// Register all path relative to static files.
		for filepath := range static.Assets() {
			r.HandleFunc(fmt.Sprintf("/static%v", filepath),
				func(w http.ResponseWriter, r *http.Request) {
					filepath := r.URL.Path[7:]
					filepathSlice := strings.Split(filepath, "/")
					filename := filepathSlice[len(filepathSlice)-1]
					// Serve correct file from blob.
					if _, ok := static.Assets()[filepath]; ok {
						http.ServeContent(w, r, filename, time.Now(),
							bytes.NewReader(static.Assets()[filepath]))
					}
				})
		}
	}

	// With all of our paths registered we'll register our mux as part of
	// the global http handler.
	http.Handle("/", r)

	if !cfg.UseLeHTTPS {
		log.Infof("Listening on %s", cfg.BindAddr)
		go http.ListenAndServe(cfg.BindAddr, r)
	} else {
		// Create a directory cache so the certs we get from Let's
		// Encrypt are cached locally. This avoids running into their
		// rate-limiting by requesting too many certs.
		certCache := autocert.DirCache("certs")

		// Create the auto-cert manager which will automatically obtain a
		// certificate provided by Let's Encrypt.
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      certCache,
			HostPolicy: autocert.HostWhitelist(cfg.Domain),
		}

		// As we'd like all requests to default to https, redirect all regular
		// http requests to the https version of the faucet.
		log.Infof("Listening on %s", cfg.BindAddr)
		go http.ListenAndServe(cfg.BindAddr, m.HTTPHandler(nil))

		// Finally, create the http server, passing in our TLS configuration.
		httpServer := &http.Server{
			Handler:      r,
			WriteTimeout: 30 * time.Second,
			ReadTimeout:  30 * time.Second,
			Addr:         ":https",
			TLSConfig: &tls.Config{
				GetCertificate: m.GetCertificate,
				MinVersion:     tls.VersionTLS12,
				CipherSuites: []uint16{
					tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
					tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
					tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				},
			},
		}
		if err := httpServer.ListenAndServeTLS("", ""); err != nil {
			log.Critical(err)
			os.Exit(1)
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}

func init() {
	// Support TLS 1.3.
	os.Setenv("GODEBUG", os.Getenv("GODEBUG")+",tls13=1")

}
