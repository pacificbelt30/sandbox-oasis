package releaseinfo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This verifies invocation/admission contracts; CI separately exercises real
// Sigstore signatures and rejection of altered artifacts and wrong identities.
func TestAttestationEnforcesPublisherWorkflowTagAndIssuer(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "gh")
	args := filepath.Join(dir, "args")
	t.Setenv("OAISO_TEST_VERIFY_ARGS", args)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OAISO_TEST_VERIFY_ARGS\"\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := verifyAttestation(context.Background(), binary, "artifact with spaces", "evidence.json", "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"attestation\nverify\nartifact with spaces\n", "--bundle\nevidence.json\n", "--repo\n" + Repository + "\n", "--source-ref\nrefs/tags/v1.2.3\n", "--cert-identity\nhttps://github.com/" + Repository + "/.github/workflows/release.yml@refs/tags/v1.2.3\n", "--cert-oidc-issuer\nhttps://token.actions.githubusercontent.com\n", "--deny-self-hosted-runners\n", "--hostname\ngithub.com\n"} {
		if !strings.Contains(text, required) {
			t.Fatal("missing signature constraint", required)
		}
	}
	// These are mutually exclusive in the real GitHub CLI. A mock verifier
	// must not let an unsupported combination satisfy the admission test.
	selectors := 0
	for _, argument := range strings.Split(text, "\n") {
		switch argument {
		case "--cert-identity", "--cert-identity-regex", "--signer-repo", "--signer-workflow":
			selectors++
		}
	}
	if selectors != 1 {
		t.Fatalf("GitHub CLI needs exactly one signer selector; got %d", selectors)
	}
	if err = os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = verifyAttestation(context.Background(), binary, "artifact", "bundle", "v1.2.3"); err == nil {
		t.Fatal("failed verifier accepted")
	}
	if err = VerifyAttestation(context.Background(), "artifact", "bundle", "../main"); err == nil {
		t.Fatal("non-tag identity accepted")
	}
}
