# Releasing

A release is a GitHub release of `Humbarrt/kicker` with these files (spec 10 §3):

| File | What |
|---|---|
| `release.json` | version, date, notes and every file below with its sha256 and size |
| `release.json.minisig` | the signature of `release.json` (minisign, Ed25519) |
| `kicker-linux-arm64` | the server for the Raspberry Pi |
| `kicker-linux-amd64` | the server for x86 Linux (testing) |
| `kicker-<module>-<version>-<board>.uf2` | firmware, one per module and board |
| `kicker-catalog-<version>.tar.gz` | the JSON Schemas of the module protocol (not in `release.json`) |

Only `release.json` is signed. A kicker trusts it only if the signature matches the public key
built into its binary, and every other file only if its sha256 matches `release.json`.

## One-time setup: the release key

1. Make the key pair on your own computer (not on a shared machine):

   ```sh
   make build
   bin/kicker release keygen --out ~/kicker-release-key
   ```

   It asks for a password (at least 12 characters) and writes `kicker-release.key` (the private
   key, encrypted with the password) and `kicker-release.pub`. The `minisign` tool makes the same
   files (`minisign -G -p kicker-release.pub -s kicker-release.key`); both work.

2. Commit the **public** key as `deploy/release.pub`:

   ```sh
   cp ~/kicker-release-key/kicker-release.pub deploy/release.pub
   git add deploy/release.pub && git commit -m "release: public key"
   ```

   Every release build embeds it (`version.UpdatePubKey`), so it never changes without a plan
   (see "Changing the key").

3. Put the private key and its password into the repo secrets (GitHub → Settings → Secrets and
   variables → Actions), or with the GitHub CLI:

   ```sh
   gh secret set RELEASE_KEY < ~/kicker-release-key/kicker-release.key
   gh secret set RELEASE_KEY_PASSWORD   # paste the password
   ```

4. Keep a copy of `kicker-release.key` and its password somewhere safe and offline (e.g. a
   password manager). The private key is never committed – `*.key` is in `.gitignore`.

Workflows of pull requests from forks don't get the repo secrets (GitHub's default), so only
tags pushed to this repo can be signed.

## Cutting a release

1. Bump the firmware version in `firmware/modules/<module>/platformio.ini` (`[kicker] version`)
   if the firmware changed. The server version comes from the tag.
2. Tag with the release notes as the tag message (markdown), and push the tag:

   ```sh
   git tag -a v0.5.0 --cleanup=verbatim -F notes.md
   git push origin v0.5.0
   ```

   `--cleanup=verbatim` keeps markdown headings: without it git drops every line starting
   with `#` as a comment. A tag without a message gets the notes "Kicker 0.5.0".

   A tag with a pre-release part (`v0.5.0-rc.1`) becomes a pre-release on GitHub.
3. The `release` workflow runs the tests, builds the web UI, the servers and the firmware,
   writes and signs `release.json`, checks the signature with the key built into the new
   binary, and publishes the release.

To check a release by hand:

```sh
minisign -Vm release.json -p deploy/release.pub
# or, with all files downloaded into one folder:
kicker release verify --pub deploy/release.pub --dir <folder>
```

## A test release on your computer

The same commands with a test key, e.g. to try the updater (M7 task 5) against a local server:

```sh
bin/kicker release keygen --test --out /tmp/rel --name test          # no password
PUB=$(sed -n 2p /tmp/rel/test.pub)
PKG=github.com/Humbarrt/kicker/server/internal/version
(cd server && go build -ldflags "-X $PKG.Version=0.5.0 -X $PKG.UpdateRepo=Humbarrt/kicker -X $PKG.UpdatePubKey=$PUB" -o /tmp/rel/kicker-linux-amd64 ./cmd/kicker)
/tmp/rel/kicker-linux-amd64 release manifest --version v0.5.0 --url http://localhost:9000 --out /tmp/rel/dist \
  --firmware firmware/modules /tmp/rel/kicker-linux-amd64        # build the firmware first, or leave --firmware out
cp /tmp/rel/kicker-linux-amd64 /tmp/rel/dist/
/tmp/rel/kicker-linux-amd64 release sign --key /tmp/rel/test.key --dir /tmp/rel/dist
/tmp/rel/kicker-linux-amd64 release verify --dir /tmp/rel/dist
```

## Changing the key

Kickers only accept releases signed with the key in their binary. To move to a new key, publish
one more release signed with the **old** key whose binary already contains the **new** public key;
after kickers have updated to it, sign with the new key. If the private key is ever leaked,
delete the secrets at once and do the same.
