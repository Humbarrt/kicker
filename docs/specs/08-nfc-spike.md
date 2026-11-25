# Spec 08: NFC spike – corner tags on iOS and Android

Status: **Decided** (2026-11-25, M3 task 12): option **B** on the link domain `112233444.xyz`, with **D** – forwarding to `https://kicker.local/claim` – as the no-app page. Implementation in M4 (spec 04).
Question from spec 03 §11: does tapping a corner tag with `kicker://claim?s=<server_id>&c=home_1` open the app on iOS and Android, or does the user have to open the app first?

## 1. Findings (from the platform documentation)
**iOS** (Apple, "Adding Support for Background Tag Reading"):
- iPhone XS and later read NDEF tags in the background, without an app open, but only while the phone is in use. It won't read them if the phone has never been unlocked since boot, the camera or Wallet is in use, airplane mode is on, or an app has its own NFC session open.
- Only the **first URI record** counts (well-known type `U`). It must be a **universal link** or one of the system schemes (http/https, mailto, sms, tel, facetime, maps, HomeKit).
- **Custom URL schemes are not supported**: Apple's note says to use universal links instead. A `kicker://` tag does nothing on an iPhone unless our app is open and scanning.
- The flow with a universal link: a notification appears, the user taps it, and the app associated with the link opens and gets the NDEF message. Without the app installed, Safari opens the link.
- Universal links need an `apple-app-site-association` file on a **public HTTPS domain** that Apple can fetch, plus the Associated Domains entitlement in the app. The kicker itself can't serve it: it is never on the internet.

**Android** (Android developers, "NFC basics"):
- The tag dispatch system starts the app whose intent filter matches the first record. A filter for `NDEF_DISCOVERED` with `scheme="kicker"` opens our app directly, even when it is closed. Custom schemes work.
- **From Android 16 on, http/https tags start `ACTION_VIEW`** instead of `NDEF_DISCOVERED`: an https tag opens the app only through a verified App Link (`assetlinks.json` on the same public domain), otherwise the browser.

**Tauri:** the official `tauri-plugin-nfc` (v2) reads and writes NDEF tags on Android and iOS (iOS 14+, `NFCReaderUsageDescription`). That covers "Program corner tags" and an in-app "Join" scan. Background launches come from the OS link handling (universal link / App Link / scheme), not from the plugin.

## 2. Options for the tag content
| | A: `kicker://claim?…` (spec 03 now) | B: `https://<project domain>/c?s=…&c=…` | C: tag ID only + in-app scan |
|---|---|---|---|
| Android, app installed | opens the app ✓ | opens the app via App Link ✓ | needs the app open |
| iPhone, app installed | **nothing**; open the app, tap "Join", scan | notification → app ✓ | open the app, "Join", scan |
| No app installed | nothing | browser opens a static page (could hand over to the kicker's web UI) | nothing |
| Needs | nothing | a domain + static hosting of two files (`apple-app-site-association`, `assetlinks.json`); no kicker server on the internet | nothing |
| Forks / own builds | work | need their own domain or fall back to "Join" | work |

## 3. Decision
**B with D** (owner, 2026-11-25): tags hold `https://112233444.xyz/c?s=<server_id>&c=<corner>`. It is the only option where one tap on an iPhone does the claim. With the app installed the phone opens the app; without it the page forwards to `https://kicker.local/claim?…`, so a phone with only a browser can join too. The domain (a numeric .xyz, registered for 10 years) is hosted on Cloudflare Pages and serves static files only (`linksite/`), so the rule that the kicker is never exposed to the internet (spec 00) still holds. The in-app "Join" scan stays for phones without background tag reading.
Option D on its own – tags with `https://kicker.local/…` – was considered: it needs no domain, but can never open the app (Apple's CDN can't reach `kicker.local` to verify a universal link) and breaks when the kicker has to call itself `kicker-2.local`.

## 4. Phone test for the owner (≈ 20 minutes)
Needs: 2–3 NTAG213/215 stickers, an iPhone (XS or newer) and/or an Android phone, the free app *NFC Tools* (iOS/Android) to write tags.
1. **Tag 1** – URI record `kicker://claim?s=test&c=home_1`. **Tag 2** – URI record `https://example.com/c?s=test&c=home_1`. Don't lock them.
2. iPhone, screen on and unlocked, no NFC app open: tap tag 1, then tag 2. Expected: tag 1 nothing; tag 2 a notification that opens Safari. Note the time from touch to notification.
3. Android: same. Expected: tag 1 "no app supports this tag" (or nothing); tag 2 opens the browser.
4. **Range at the table:** put tag 2 where the corner will be, under the planned material (terrazzo/print/foam thickness as designed) and tap from the player's position. Note the maximum distance, whether the phone case matters, and any metal nearby (it kills NFC).
5. Write down the phone models and OS versions.

Steps 1–3 only confirm the documentation; step 4 (read range through the corner material) matters for the hardware and is done with the corner design. Record the results here (§5).

## 5. Results
*(owner, after the test)*
