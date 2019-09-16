package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8081", "address to listen on")
	webhook := flag.String("webhook", "", "slack webhook")
	flag.Parse()
	s := NewSlack(*webhook)
	panic(http.ListenAndServe(*addr,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec != nil {
					fmt.Println("panic:", rec)
					panic(rec)
				}
			}()
			data := map[string]interface{}{}
			err := json.NewDecoder(r.Body).Decode(&data)
			if err != nil {
				fmt.Println("error:", err.Error())
				return
			}
			switch data["type"] {
			case "comment-added":
				owner := data["change"].(map[string]interface{})["owner"].(map[string]interface{})["username"].(string)
				author := data["author"].(map[string]interface{})["username"].(string)
				if owner != author {
					s.notify(owner,
						fmt.Sprintf("%s left a comment on %s",
							data["author"].(map[string]interface{})["name"].(string),
							change(data["change"])))
				}
			case "reviewer-added":
				s.notify(data["reviewer"].(map[string]interface{})["username"].(string),
					fmt.Sprintf("Your review has been requested on %s",
						change(data["change"])))
			case "reviewer-deleted":
			case "ref-updated":
			default:
				fmt.Printf("unknown event type: %v\n", data["type"])
			}
		})))
}

func change(c interface{}) string {
	casted := c.(map[string]interface{})
	subject := casted["subject"].(string)
	owner := casted["owner"].(map[string]interface{})["name"].(string)
	url := casted["url"].(string)
	return fmt.Sprintf("<%s|%s (%s)>", url, subject, owner)
}

func (s *Slack) notify(user, message string) {
	channel, found := map[string]string{
		"zeebo":  "@jeff",
		"jtolds": "@jt",
	}[user]
	if !found {
		fmt.Printf("no slack username found for %q\n", user)
		s.Message("@jt", fmt.Sprintf("slack username missing for %q", user))
		return
	}
	fmt.Printf("sending %s: %s\n", channel, message)
	err := s.Message(channel, message)
	if err != nil {
		fmt.Println("error:", err)
	}
}
