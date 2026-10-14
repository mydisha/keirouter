package crypto

import "testing"

func TestPortableBoxRoundTripAndWrongPassphrase(t *testing.T) {
	box, err := NewPortableBox("correct horse", nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := box.SealString("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	salt, err := DecodePortableSalt(box.Salt())
	if err != nil {
		t.Fatal(err)
	}
	again, _ := NewPortableBox("correct horse", salt)
	if pt, err := again.OpenString(ct); err != nil || pt != "sk-secret" {
		t.Fatalf("open = %q, %v", pt, err)
	}
	wrong, _ := NewPortableBox("wrong", salt)
	if _, err := wrong.OpenString(ct); err == nil {
		t.Fatal("wrong passphrase must fail")
	}
	ct2, _ := box.SealString("sk-secret")
	if ct2 == ct {
		t.Fatal("nonce must be fresh per seal")
	}
}
