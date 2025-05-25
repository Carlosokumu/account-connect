package messages

import "encoding/json"

type AccountConnectMsg struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type CtConnectMsg struct {
	AccountId    int64  `json:"account_id"`
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AccessToken  string `json:"access_token"`
}
