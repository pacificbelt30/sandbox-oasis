# Verify distribution signatures and provenance

[日本語](../../guides/signed-releases.md) | **English**

- Available: v0.0.4 or later.
- The installer and `upgrade` reject unsigned online releases.

| Evidence | Policy |
|---|---|
| Signature | GitHub OIDC/Sigstore, using official GitHub CLI trusted roots; never trust keys supplied by the artifact |
| Built source | All three executables must identify the current clean checkout commit in Go build metadata |
| Publisher | Exact `pacificbelt30/sandbox-oasis/.github/workflows/release.yml` and selected `refs/tags/<version>` |
| Runner | GitHub-hosted; reject self-hosted provenance |
| Subjects | Archive, installer, SHA256SUMS and SBOM |
| Portable evidence | `provenance.json` signature/certificate/transparency verification bundle |
| Inventory | CycloneDX 1.6, actual linked Go modules, Go standard library and pinned downloaded runtime assets |
| Inventory exclusions | Guest OCI image packages and host OS packages |
| Rollback floor | Root-owned `release-floor.json` retains the highest installed release |

Checksums detect corruption; they do not replace signatures. CI obtains short-lived signing certificates, without a persistent publisher private key. Compromise of GitHub, Sigstore, workflows or dependencies remains outside this guarantee.

## Prerequisites

- Official [GitHub CLI for Linux](https://github.com/cli/cli/blob/trunk/docs/install_linux.md), supporting `gh attestation verify --bundle --source-ref --cert-identity`. Needed for signed installation/upgrade, not ordinary VM use.
- Existing [installer prerequisites](../installation.md): Bash, curl, CA certificates and uv-managed Python. No Go/Make required for prebuilt installation.
- Local evidence bundles avoid a GitHub login requirement. Trusted-root refresh may use the network.
- Missing tools/evidence, altered signatures, unexpected signer/workflow/tag fail before placement.

## Verify the installer before executing it

Run as a normal user. This block verifies v0.0.6 and displays the installation plan.

```bash
export RELEASE_TAG=v0.0.6
bash <<'SH'
set -euo pipefail
: "${RELEASE_TAG:?Export the signed release tag first}"
oaiso_install_dir=$(mktemp -d)
trap 'rm -rf -- "$oaiso_install_dir"' EXIT
base="https://github.com/pacificbelt30/sandbox-oasis/releases/download/$RELEASE_TAG"
curl -fLsS --proto '=https' --proto-redir '=https' "$base/install.sh" -o "$oaiso_install_dir/install.sh"
curl -fLsS --proto '=https' --proto-redir '=https' "$base/provenance.json" -o "$oaiso_install_dir/provenance.json"
gh attestation verify "$oaiso_install_dir/install.sh" \
  --bundle "$oaiso_install_dir/provenance.json" \
  --repo pacificbelt30/sandbox-oasis \
  --source-ref "refs/tags/$RELEASE_TAG" \
  --cert-identity "https://github.com/pacificbelt30/sandbox-oasis/.github/workflows/release.yml@refs/tags/$RELEASE_TAG" \
  --cert-oidc-issuer https://token.actions.githubusercontent.com \
  --deny-self-hosted-runners --hostname github.com
bash "$oaiso_install_dir/install.sh" --version "$RELEASE_TAG"
SH
```

Success verifies the script and displays a plan. To apply, add `--apply` to the final installer invocation while retaining the verified tag. A plain `curl | bash` trusts the HTTPS bootstrap script before running it; the block above verifies that script first. `oaiso upgrade` also verifies the downloaded installer before execution.

| Operation or failure | Behavior |
|---|---|
| Older release | Reject before stopping services |
| Deliberate older signed release | Explicit `--allow-downgrade` on upgrade/installer/setup; keep the high-water mark |
| Failed installation | Existing automatic transactional recovery |
| `setup --rollback --apply` | Explicit recovery; retain the high-water mark |
| Explicit `--bundle PATH` | Trusted local source-built/offline bundle; no online distribution signature check |
| Offline distribution transfer | Verify signatures/hashes first and retain evidence; see [official offline trust-root preparation](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/verify-attestations-offline) |
| Root replaces floor/verifier/trust roots | Trusted operator action; local records cannot prevent it |

A valid signature does not guarantee absence of vulnerabilities, reproducible builds or guest image safety. Main CI creates real test signatures and rejects altered artifacts/wrong workflows/wrong tags; those CI signatures cannot satisfy release admission policy.

See [official attestations](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations), [verification CLI](https://cli.github.com/manual/gh_attestation_verify) and [test evidence](../../testing.md).
