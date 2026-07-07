package valid

import "testing"

func TestValidateUsername(t *testing.T) {
	valid := []string{"ab", "alice", "bob_smith", "user-1", "a1", "x0y9z", "nova99"}
	for _, u := range valid {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("ValidateUsername(%q) = %v, want nil", u, err)
		}
	}

	invalid := []string{
		"",           // empty
		"a",          // too short
		"_bob",       // leading underscore
		"bob_",       // trailing underscore
		"-bob",       // leading dash
		"Alice",      // uppercase
		"bob smith",  // space
		"bob.smith",  // dot
		"bob@host",   // at
		"../etc",     // path chars
		"<script>",   // html
		"root",       // reserved
		"admin",      // reserved
		"guest",      // reserved (unauth flow)
		"noreply",    // reserved
		"thisusernameiswaytoolongtobeacceptable", // > 32
	}
	for _, u := range invalid {
		if err := ValidateUsername(u); err == nil {
			t.Errorf("ValidateUsername(%q) = nil, want error", u)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Error("short password accepted")
	}
	if err := ValidatePassword("goodenough1"); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}
	long := make([]byte, MaxPasswordLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidatePassword(string(long)); err == nil {
		t.Error("over-long password accepted")
	}
}

func TestValidateEmail(t *testing.T) {
	ok, err := ValidateEmail("  Alice@Example.COM ")
	if err != nil {
		t.Fatalf("valid email rejected: %v", err)
	}
	if ok != "alice@example.com" {
		t.Errorf("normalized = %q, want alice@example.com", ok)
	}

	bad := []string{
		"", "notanemail", "a@", "@b.com", "a@b",
		"two@addr@x.com", "Name <a@b.com>", // display-name form rejected
		"a@mailinator.com", "x@guerrillamail.com", // disposable
	}
	for _, e := range bad {
		if _, err := ValidateEmail(e); err == nil {
			t.Errorf("ValidateEmail(%q) = nil, want error", e)
		}
	}
}
