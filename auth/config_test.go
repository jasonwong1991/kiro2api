package auth

import "testing"

func TestProcessConfigs_DefaultAuthTypeIsIdC(t *testing.T) {
	input := []AuthConfig{
		{
			RefreshToken: "rt_idc_default",
			ClientID:     "cid",
			ClientSecret: "csecret",
		},
	}

	got := processConfigs(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 valid config, got %d", len(got))
	}
	if got[0].AuthType != AuthMethodIdC {
		t.Fatalf("expected default auth type %q, got %q", AuthMethodIdC, got[0].AuthType)
	}
}

