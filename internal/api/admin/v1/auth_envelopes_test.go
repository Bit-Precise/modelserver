package adminv1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthTokensBodyNormalLoginShape(t *testing.T) {
	t.Parallel()
	user := &User{ID: "u1", Email: "a@b", Nickname: "A"}
	body := authTokensBody{AccessToken: "at", RefreshToken: "rt", User: user}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(encoded)
	for _, needle := range []string{`"access_token":"at"`, `"refresh_token":"rt"`, `"user":{`} {
		if !strings.Contains(got, needle) {
			t.Errorf("JSON %s does not contain %s", got, needle)
		}
	}
	if strings.Contains(got, "redirect_to") {
		t.Errorf("normal-login body leaked redirect_to: %s", got)
	}
}

func TestAuthTokensBodyHydraRedirectShape(t *testing.T) {
	t.Parallel()
	body := authTokensBody{RedirectTo: "https://example.com/callback?auth_token=xyz"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(encoded)
	if !strings.Contains(got, `"redirect_to":"https://example.com/callback?auth_token=xyz"`) {
		t.Fatalf("missing redirect_to; got: %s", got)
	}
	for _, forbidden := range []string{"access_token", "refresh_token", `"user"`} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Hydra-redirect body leaked %s: %s", forbidden, got)
		}
	}
}
