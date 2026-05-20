package main

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/v1/chat", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "gateway alive — proxy coming next")
	})

	fmt.Println("gateway listening on :8080")

	http.ListenAndServe(":8080", r)
}