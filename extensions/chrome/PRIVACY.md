# Privacy Policy — Bolt Capture (Chrome Extension)

**Last updated:** 2026-03-05

## Data Collection

Bolt Capture does not collect, store, or transmit any personal data. The extension does not use analytics, telemetry, or tracking of any kind.

## What the Extension Does

- Intercepts browser downloads and forwards them to the Bolt download manager running on your local machine (`127.0.0.1`).
- Reads cookies for download URLs so Bolt can authenticate with the remote server on your behalf. Cookies are sent only to your local Bolt instance, never to any external server.
- Stores your preferences (server URL, auth token, filters) locally in your browser using `chrome.storage.local`. This data never leaves your machine.

## Network Communication

All communication is between the extension and Bolt's HTTP server on `127.0.0.1` (localhost). No data is sent to any external server, third party, or remote endpoint.

## Contact

If you have questions about this privacy policy, open an issue at https://github.com/fhsinchy/bolt/issues.
