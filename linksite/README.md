# Link site – 112233444.xyz

The public domain on the corner tags (spec 03 §11, spec 08). It serves **static files only**; the
kicker itself is never on the internet.

- `https://112233444.xyz/c?s=<server_id>&c=<corner>` is what every corner tag holds.
  - Kicker app installed: the phone opens the app (iOS universal link / Android App Link) and this site isn't loaded.
  - No app: `c.html` forwards to `https://kicker.local/claim?s=…&c=…`, the kicker's web UI on the local network (the `/claim` page comes in M4).
- `.well-known/apple-app-site-association` and `.well-known/assetlinks.json` tie the domain to the app. They hold placeholders until M4 (`TEAMID.BUNDLE_ID`, `PACKAGE_NAME`, `SIGNING_CERT_SHA256`).

## Deploy (Cloudflare Pages)
1. Cloudflare → Workers & Pages → Create → Pages → upload the `public/` folder (or connect the repo with build output directory `linksite/public`, no build command).
2. Custom domain: `112233444.xyz` – the bare domain, no `www`, and no redirect between the two.
3. Security settings for this domain: Bot Fight Mode **off**, no "Under Attack" mode, no challenge on `/.well-known/*` – Apple's and Google's fetchers must get the files, not a challenge page.
4. Never add a DNS record or tunnel to the kicker.

## Check after every deploy
```sh
curl -sI https://112233444.xyz/.well-known/apple-app-site-association   # 200, content-type: application/json, no location:
curl -sI https://112233444.xyz/.well-known/assetlinks.json              # same
curl -sI "https://112233444.xyz/c?s=test&c=home_1"                      # 200 (not a redirect to /c/ or /c.html)
```
In M4, also Apple's CDN view: `curl -s https://app-site-association.cdn-apple.com/a/v1/112233444.xyz`.

## M4 to-do
- Fill in the Apple Team ID + bundle ID and the Android package name + signing certificate SHA-256 (the Play App Signing key if the app is distributed through Google Play). Android package names can't have a segment starting with a digit, so the app ID can't simply be the reversed domain (`xyz.112233444…`).
- The kicker's `/claim` page: claims the corner with the browser's login (or asks to log in first), checks that `s` is this kicker's `server_id`.
