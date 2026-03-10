package main

import (
	"html/template"
	"log"
)

func main() {
	_, err := template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Template Error: %v", err)
	}
	log.Println("Parsed successfully")
}
