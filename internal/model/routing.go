package model

import "time"

// Android channel IDs are part of the shipped Other and Sample APKs.
type RoutingChannel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var RoutingChannels = []RoutingChannel{
	{ID: "alerts", Label: "Alerts"},
	{ID: "system", Label: "System"},
}

func ValidChannel(id string) bool {
	for _, channel := range RoutingChannels {
		if channel.ID == id {
			return true
		}
	}
	return false
}

type RoutingTopic struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Channel    string     `json:"channel"`
	ArchivedAt *time.Time `json:"archived_at"`
}
