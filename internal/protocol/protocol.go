// Package protocol defines the control messages exchanged between the
// sawt daemon and the Android app over WebSocket.
package protocol

import (
	"encoding/json"
	"fmt"
)

const Version = 1

type Message struct {
	Type     string  `json:"type"`
	Version  int     `json:"version,omitempty"`
	ID       string  `json:"id,omitempty"`
	Name     string  `json:"name,omitempty"`
	Token    string  `json:"token,omitempty"`
	Code     string  `json:"code,omitempty"`
	Client   string  `json:"client,omitempty"`
	Rate     int     `json:"rate,omitempty"`
	Channels int     `json:"channels,omitempty"`
	Format   string  `json:"format,omitempty"`
	FrameMS  int     `json:"frame_ms,omitempty"`
	Pairing  bool    `json:"pairing,omitempty"`
	RTT      int64   `json:"rtt_ms,omitempty"`
	Dropped  uint64  `json:"dropped,omitempty"`
	Clients  int     `json:"clients,omitempty"`
	Gain     float64 `json:"gain,omitempty"`
	Mute     bool    `json:"mute,omitempty"`
	Level    float64 `json:"level,omitempty"`
	Frames   uint64  `json:"frames,omitempty"`
	Bytes    uint64  `json:"bytes,omitempty"`
	Error    string  `json:"error,omitempty"`
	T        int64   `json:"t,omitempty"`
}

func Encode(m Message) ([]byte, error) {
	return json.Marshal(m)
}

func Decode(b []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("decode control message: %w", err)
	}
	return m, nil
}

func Hello(token, client string) Message {
	return Message{Type: "hello", Version: Version, Token: token, Client: client}
}

func Pair(code, client string) Message {
	return Message{Type: "pair", Version: Version, Code: code, Client: client}
}

func Ping(t int64) Message {
	return Message{Type: "ping", T: t}
}

func Pong(t int64) Message {
	return Message{Type: "pong", T: t}
}
