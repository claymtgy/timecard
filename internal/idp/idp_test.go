package idp

import (
	"encoding/json"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword(h, "wrong password!!") {
		t.Fatal("wrong password verified")
	}
	if VerifyPassword("garbage", "x") || VerifyPassword("$argon2id$v=19$m=1,t=1,p=1$$", "x") {
		t.Fatal("malformed hash verified")
	}
}

func TestAuthRequestRoundTrip(t *testing.T) {
	maxAge := uint(300)
	req := &oidc.AuthRequest{
		ClientID: "crm-web", RedirectURI: "http://localhost:5173/auth/callback", State: "st",
		Scopes: oidc.SpaceDelimitedArray{"openid", "email"}, ResponseType: oidc.ResponseTypeCode,
		Nonce: "n", CodeChallenge: "abc", CodeChallengeMethod: oidc.CodeChallengeMethodS256,
		Prompt: oidc.SpaceDelimitedArray{"login", "bogus"}, MaxAge: &maxAge,
	}
	ar := newAuthRequest(req, "id-1")
	b, err := json.Marshal(ar)
	if err != nil {
		t.Fatal(err)
	}
	var back authRequest
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.GetClientID() != "crm-web" || back.GetNonce() != "n" || back.GetState() != "st" || len(back.GetScopes()) != 2 {
		t.Fatalf("round trip lost fields: %+v", back)
	}
	if cc := back.GetCodeChallenge(); cc == nil || cc.Method != oidc.CodeChallengeMethodS256 {
		t.Fatalf("code challenge: %+v", cc)
	}
	if !back.hasPrompt("login") || back.hasPrompt("bogus") || *back.MaxAgeSeconds != 300 {
		t.Fatalf("prompt/max_age: %+v", back)
	}
	if back.Done() || back.GetAMR() != nil {
		t.Fatal("new request must not be done")
	}
}
