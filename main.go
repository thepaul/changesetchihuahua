package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/kr/pretty"
)

func main() {
	addr := flag.String("addr", ":8081", "address to listen on")
	flag.Parse()
	panic(http.ListenAndServe(*addr,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pretty.Println(r.Method)
			pretty.Println(r.URL)
			pretty.Println(r.Header)
			data, err := ioutil.ReadAll(r.Body)
			if err != nil {
				fmt.Println("error:", err.Error())
				return
			}
			fmt.Println(string(data))
		})))
}
