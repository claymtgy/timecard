package idp

import (
	"encoding/json"
	"strings"
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

func TestOrgPassword(t *testing.T) {
	o := &Orgs{key: []byte("test-key")}
	if o.hash("  abcd-efgh-jkmn ") != o.hash("ABCD-EFGH-JKMN") {
		t.Fatal("hash should ignore case and surrounding space")
	}
	if o.hash("ABCD-EFGH-JKMN") == (&Orgs{key: []byte("other")}).hash("ABCD-EFGH-JKMN") {
		t.Fatal("hash must depend on the server key")
	}
	a, b := GenerateOrgPassword(), GenerateOrgPassword()
	if a == b || len(a) != 19 || validateOrgPassword(a) != nil {
		t.Fatalf("generated %q %q", a, b)
	}
	if validateOrgPassword("short") == nil {
		t.Fatal("short password accepted")
	}
}

// The app refuses logins without these claims, so they must survive into the ID token.
func TestOrgClaimsReachIDToken(t *testing.T) {
	info := new(oidc.UserInfo)
	info.AppendClaims("org_id", "abc")
	info.AppendClaims("org_name", "Acme")
	claims := new(oidc.IDTokenClaims)
	claims.SetUserInfo(info)
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["org_id"] != "abc" || got["org_name"] != "Acme" {
		t.Fatalf("claims missing: %s", b)
	}
}

func TestOrgPasswordEncryption(t *testing.T) {
	o := &Orgs{key: []byte("test-key")}
	enc, err := o.encrypt(" abcd-efgh-jkmn ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "abcd") {
		t.Fatal("not encrypted")
	}
	if got, err := o.decrypt(enc); err != nil || got != "abcd-efgh-jkmn" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := (&Orgs{key: []byte("other")}).decrypt(enc); err == nil {
		t.Fatal("decrypted with the wrong key")
	}
	if again, _ := o.encrypt("abcd-efgh-jkmn"); again == enc {
		t.Fatal("encryption must not be deterministic")
	}
}
