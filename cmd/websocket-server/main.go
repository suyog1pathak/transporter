package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{} //
var wg sync.WaitGroup

type Client struct {
	Connection *websocket.Conn
	Send       chan []byte
}

func (c *Client) ReadMessages() {
	for {
		_, message, err := c.Connection.ReadMessage()
		if err != nil {
			fmt.Println("Error reading message")
			return
		}
		fmt.Println("Message received - ", message)
	}

}

func (c *Client) WriteMessages() {
	for msg := range c.Send {
		err := c.Connection.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			fmt.Println("Error writing message")
			return
		}
	}

}

func IfExistsIn(data map[string]*Client, input string) bool {
	for x := range data {
		if x == input {
			return true
		}
	}
	return false
}

func main() {
	connections := make(map[string]*Client)
	http.HandleFunc("/connection", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Print("upgrade:", err)
			return
		}
		defer c.Close()
		// Give me the identity
		err = c.WriteMessage(websocket.TextMessage, []byte(string("I need your identity")))
		if err != nil {
			log.Println("read:", err)

		}
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
		}
		// save the identity with connection
		client := &Client{
			Connection: c,
			Send:       make(chan []byte),
		}
		connections[string(message)] = client

		err = c.WriteMessage(mt, []byte(string("I have saved your connection in my pool")))
		if err != nil {
			log.Println("write:", err)

		}
		wg.Add(1)
		go client.ReadMessages()
		go client.WriteMessages()
		fmt.Println("Connections ---->", connections)

		events := []string{"agent-2", "agent-3", "agent-1"}

		for k := range connections {
			fmt.Println("---->", k)
		}

		for _, event := range events {
			fmt.Println("Event for ------", event)
			if IfExistsIn(connections, event) {
				socket := connections[event]
				message := []byte(string("hey there I am for agent-1"))
				socket.Send <- message
			}
		}

		wg.Wait()
	})

	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
