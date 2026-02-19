package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check for marker file from base image
		if _, err := os.Stat("/base-image-marker"); err == nil {
			fmt.Fprintln(w, "Base image marker found!")
		} else {
			fmt.Fprintln(w, "Base image marker NOT found!")
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("Listening on port %s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
