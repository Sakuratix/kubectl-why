package kube

import (
	"strings"
	"testing"
)

func TestExplain(t *testing.T) {
	cases := map[string]string{
		"The connection to the server localhost:8080 was refused - did you specify the right host or port?": "can't reach the cluster API",
		"Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout":                              "can't reach the cluster API",
		"error: You must be logged in to the server (Unauthorized)":                                         "credentials",
		`Error from server (Forbidden): events is forbidden: User "dev" cannot list resource "events"`:      "RBAC",
		`Error from server (NotFound): pods "api" not found`:                                                "-n NAMESPACE",
	}
	for msg, want := range cases {
		got := Explain(msg).Error()
		if !strings.HasPrefix(got, msg) || !strings.Contains(got, "hint: ") || !strings.Contains(got, want) {
			t.Errorf("Explain(%q) = %q, want the message plus a hint about %q", msg, got, want)
		}
	}
	if got := Explain("something else").Error(); got != "something else" {
		t.Errorf("unknown errors must pass through, got %q", got)
	}
}
