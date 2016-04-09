package main

import (
	"net/http"

	log "github.com/Sirupsen/logrus"
)

type handler func(w http.ResponseWriter, r *http.Request)

func auth(pass handler) handler {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Please authenticate for uploading"`)

		user, password, _ := r.BasicAuth()
		if user != FlagSecretUser || password != FlagSecretPassword {
			log.Error("authorization failed")
			http.Error(w, "authorization failed", http.StatusUnauthorized)
			return
		}

		pass(w, r)
	}
}
