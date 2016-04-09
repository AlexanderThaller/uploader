package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", handler)
	http.ListenAndServe(":8080", nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("handling req...")

	//parse the multipart stuff if there
	err := r.ParseMultipartForm(15485760)

	//
	if err == nil {
		defer r.MultipartForm.RemoveAll()
		fmt.Printf("%d files", len(r.MultipartForm.File))
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Println(err.Error())
	}

	//fmt.Fprintf(w, "Hi there, I love %s!", r.URL.Path[1:])
	fmt.Println("leaving...")
}
