package main

import (
	"log"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

var wg sync.WaitGroup

func main() {
	u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/connection"}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer close(done)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("recv: %s", message)
		}
	}()
	err = c.WriteMessage(websocket.TextMessage, []byte(string("agent-1")))
	if err != nil {
		log.Println("write:", err)
		return
	}
	wg.Wait()
}
