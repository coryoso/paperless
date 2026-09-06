# Apple code signing and notarization

Paperless release builds support Developer ID signing and Apple notarization. Signing happens on GitHub-hosted macOS runners before the archives and Homebrew checksum are created, so GitHub releases and Homebrew install the same signed bytes.

## Apple prerequisites

You need an active Apple Developer Program membership, a `Developer ID Application` certificate exported from Keychain Access as a password-protected `.p12` file, and an App Store Connect API key with access to notarization. Download the API key's `.p8` file when it is created; Apple allows it to be downloaded only once.

## Repository secrets

Add these Actions secrets to the Paperless repository:

| Secret | Value |
| --- | --- |
| `APPLE_DEVELOPER_ID_CERTIFICATE_BASE64` | Base64-encoded `.p12` certificate |
| `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD` | Password used when exporting the `.p12` |
| `APPLE_NOTARY_KEY_BASE64` | Base64-encoded App Store Connect `.p8` key |
| `APPLE_NOTARY_KEY_ID` | App Store Connect API key ID |
| `APPLE_NOTARY_ISSUER_ID` | App Store Connect issuer ID |

Encode the two files without placing their contents in shell history:

```bash
base64 -i DeveloperIDApplication.p12 | pbcopy
gh secret set APPLE_DEVELOPER_ID_CERTIFICATE_BASE64

base64 -i AuthKey_KEYID.p8 | pbcopy
gh secret set APPLE_NOTARY_KEY_BASE64
```

Enter the copied value at each prompt, then add the remaining values with `gh secret set SECRET_NAME`.

The workflow publishes unsigned archives with a visible warning until the two certificate secrets are present. After one signed release succeeds, set the repository Actions variable `REQUIRE_APPLE_SIGNING` to `true`; subsequent releases will fail closed instead of publishing unsigned binaries when the certificate is missing.

Notarization is independent: signed archives are allowed with a warning until all three notary secrets are present. After notarization succeeds, set `REQUIRE_APPLE_NOTARIZATION` to `true` so a release cannot proceed without Apple accepting it.

## Release behavior

For every published GitHub release, the workflow:

1. Builds the embedded web interface and runs the Go tests.
2. Cross-compiles Apple silicon and Intel binaries with local paths removed.
3. Imports the certificate into a temporary runner keychain.
4. Signs every distributed executable with the hardened runtime and a secure timestamp.
5. Submits the signed universal archive to Apple's notarization service and waits for acceptance.
6. Packages the signed binaries, publishes checksums, and updates the Homebrew formula.
7. Deletes temporary certificate, key, and keychain files even when a step fails.

Apple does not support stapling a notarization ticket directly to a standalone command-line executable. Gatekeeper retrieves the accepted notarization ticket online when necessary; code signatures can be checked locally with `codesign --verify --strict --verbose=2 /path/to/paperless`.
