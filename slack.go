package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

type Slack struct {
	webhook string
}

func NewSlack(webhook string) *Slack {
	return &Slack{webhook: webhook}
}

func (s *Slack) Message(msg string) error {
	type body struct {
		Text string `json:"text"`
	}
	data, err := json.Marshal(body{Text: msg})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.webhook, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response from slack: %v", resp.Status)
	}
	data, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if string(data) != "ok" {
		return fmt.Errorf("unexpected response from slack: %v", string(data))
	}
	return nil
}
