package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, e := HashPassword("a long correct horse password 123")
	if e != nil {
		t.Fatal(e)
	}
	if !VerifyPassword(hash, "a long correct horse password 123") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password accepted")
	}
}
func TestPasswordLimits(t *testing.T) {
	if _, e := HashPassword("short"); e == nil {
		t.Fatal("short password accepted")
	}
	tooLong := make([]byte, maxPasswordBytes+1)
	if _, e := HashPassword(string(tooLong)); e == nil {
		t.Fatal("oversized password accepted")
	}
}

func TestRegistrationPasswordRule(t *testing.T) {
	for _, tc := range []struct {
		name, password string
	}{
		{"too short", "abc12"},
		{"no number", "abcdef"},
		{"no letter", "123456"},
		{"symbols and number", "!@#$%1"},
		{"multibyte too short", "éééé1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := HashPassword(tc.password); err == nil {
				t.Fatal("invalid registration password accepted")
			}
		})
	}
	hash, err := HashPassword("abcde1")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "abcde1") {
		t.Fatal("valid six-character registration password rejected")
	}
}
