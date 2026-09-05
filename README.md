# AI Finance Controller
LIVE LINK : https://ai-finance-controller-836608396664.us-central1.run.app/
Reconciles internal ledger, Razorpay/gateway settlement, and bank
statement data for a synthetic 52-record batch — deterministic matching
first, Gemini (via Vertex AI) only for the handful of genuinely
ambiguous cases. See [CLAUDE.md](CLAUDE.md) for the full build log and
design rationale.

## Run from a fresh clone

Requires Go 1.26+.

```bash
go run ./cmd/server
```

Open **http://localhost:8080** and click **Run Reconciliation**. The
synthetic fixtures in `data/fixtures/` are checked into the repo, so
this works immediately with no setup — the AI judge is optional (see
below).

Run the test suite any time with:

```bash
go test ./...
```

## AI judge credentials (optional)

The deterministic engine resolves ~92% of the batch on its own. The
remaining ambiguous orders are sent to Gemini via Vertex AI — this
requires a GCP project, but the app runs correctly without one.

**With a GCP project:**

1. Copy `.env.example` to `.env` and fill in `GOOGLE_CLOUD_PROJECT` and
   `GOOGLE_CLOUD_LOCATION`.
2. Pick **one** auth path and stick to it:
   - **API key** — set `GOOGLE_API_KEY` in `.env`.
   - **Application Default Credentials** — leave `GOOGLE_API_KEY` unset
     and run `gcloud auth application-default login` first. Re-run this
     the same day you demo — cached ADC tokens expire.
3. `GEMINI_MODEL` is optional (defaults to `gemini-2.5-flash`).

**Without credentials:** just run the server as-is. `cmd/server/main.go`
logs a warning at startup and disables the judge — ambiguous orders are
reported honestly as pending (`AMBIGUOUS`, category
`POSSIBLE_DUPLICATE`) instead of the run failing. This is a legitimate
mode, not a degraded one: the dashboard, exceptions, and metrics all
still work end to end.

## Deploying to Cloud Run

Serverless containers, not something to administer — matches the
"single binary, no infra we don't need" philosophy (no GKE, no manual
VM patching). `GET /healthz` (always 200, independent of pipeline
state) is Cloud Run's startup/liveness target; `cmd/server/main.go`
handles `SIGTERM` gracefully (`http.Server.Shutdown`, in-flight
requests finish before the process exits) and reads `$PORT`, which
Cloud Run sets automatically.

**Auth: Workload Identity, not an API key.** Grant the Cloud Run
service's own service account `roles/aiplatform.user`, then set only
`GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`, and (optionally)
`GEMINI_MODEL` as Cloud Run environment variables — leave
`GOOGLE_API_KEY` **unset**. `judge.NewGeminiProviderFromEnv` already
falls back to Application Default Credentials when no API key is
configured, and on Cloud Run, ADC resolves to the attached service
account automatically — no Secret Manager entry, no long-lived
credential material in the environment, and no ADC-token-expiry risk
to babysit before a demo. (The API-key code path itself stays in the
codebase for local development on a machine without `gcloud` set up —
it's opt-in and does nothing unless `GOOGLE_API_KEY` is explicitly set,
so there's nothing to "remove" for the hosted deployment to be
secure; the security property comes from what env vars you set in
Cloud Run, not from deleting an unused local-dev convenience.)

```bash
# one-time IAM grant (replace PROJECT_ID and the Cloud Run service account)
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:PROJECT_NUMBER-compute@developer.gserviceaccount.com" \
  --role="roles/aiplatform.user"

# build + deploy from source (Cloud Run's buildpacks pick up the Dockerfile)
gcloud run deploy ai-finance-controller \
  --source . \
  --region us-central1 \
  --allow-unauthenticated \
  --set-env-vars GOOGLE_CLOUD_PROJECT=PROJECT_ID,GOOGLE_CLOUD_LOCATION=us-central1,GEMINI_MODEL=gemini-2.5-flash
```

**During the actual judging window**, set `--min-instances=1` (small,
predictable cost) so nothing hits a cold start mid-demo; scale back to
0 afterward:

```bash
gcloud run services update ai-finance-controller --region us-central1 --min-instances=1
# ...after judging:
gcloud run services update ai-finance-controller --region us-central1 --min-instances=0
```

**Known trade-off, not a bug:** all state (the cached report, the
resolution log) is in-memory and resets if the instance scales to
zero between visits. `--min-instances=1` during judging avoids this
being visible. This is deliberate — a stateless demo deployment
doesn't need a database, and adding one to "fix" a reset that only
matters between judging sessions would violate the project's own "no
infrastructure we don't need" rule.

The default `*.run.app` HTTPS URL is fine to demo with directly.

Verified locally before documenting these steps: `docker build .`
produces a 43.6MB image (distroless base, CGO-free static binary — no
cgo dependency in this module), the container serves `/healthz`, the
embedded dashboard, and a live `/reconcile/run` against the bundled
fixtures correctly, and `docker stop` (which sends a real SIGTERM)
triggers the graceful-shutdown log line as expected.

## API

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Always 200 — Cloud Run's startup/liveness probe |
| `GET /reconcile/run` | Run the full pipeline live, cache the result |
| `POST /reconcile/generate` | Generate a fresh synthetic batch (`size` — one of `50`/`100`/`250`, `seed` optional) and run the pipeline against it |
| `POST /reconcile/upload` | Upload your own `ledger`/`gateway`/`bank` JSON files (multipart form; `ground_truth` optional) and run the pipeline against them — 500KB/file cap, rate-limited per IP |
| `GET /reconcile/report` | Return the last cached run (409 if none yet) |
| `GET /reconcile/exceptions` | Just the exception list from the last run |
| `GET /reconcile/orders/:id` | Combined match/verdict/exception trace for one order (404 if unknown) |
| `POST /reconcile/orders/:id/resolve` | Log a human decision (`{action, reason, utr}`) against an order — `utr` names the specific tied candidate approved, if any |
| `GET /reconcile/orders/:id/resolutions` | The append-only human-decision log for one order |

The resolve/resolutions pair is a **feedback-loop seed**, not a
retraining pipeline: it logs what a human decided and why, for later
review. It never mutates the order's own match or exception state and
never moves money — the dashboard's investigation drawer (click any
exception row) is the front end for it.

## Known issues found and fixed

**Zero-confidence verdicts accepted as valid (the headline one).** The
investigation drawer showed "Gemini confidence: 0%" for the DUPLICATE
fixture pair — not a plumbing bug, a live call confirmed Gemini genuinely
returned a fully-reasoned UNRESOLVED verdict with `confidence: 0.0`.
`judge.ValidateRaw`'s range check accepted this since 0 sits on the
boundary of `[0,1]`, but a confidence of exactly zero isn't a meaningful
estimate and shouldn't be trusted at face value any more than a malformed
response. Fixed by requiring `confidence > 0`; a zero now degrades through
the same fallback path as a network failure or bad JSON. Confirmed live
after the fix shipped: a later run against the real Vertex AI API hit the
same zero-confidence response again, and this time the drawer correctly
showed "Gemini confidence unavailable" instead of a broken bar. See
CLAUDE.md's "Precision & Impressiveness Pass — Priority 0" for the full
writeup.

**"RAZORPAY" contains "PAY".** Building a continuous reference-similarity
score (so a truncated payment reference scores between "exact" and
"nothing" instead of collapsing to 0) initially handed every generic,
reference-free "RAZORPAY SETTLEMENT" description partial credit it hadn't
earned — because "RAZORPAY" itself contains the substring "PAY", which
coincidentally overlaps the start of any "PAY_xxxx" ID. Fixed by requiring
the overlap to cover at least half the payment ID's length before granting
any partial credit. See CLAUDE.md's "Precision & Impressiveness Pass —
Priority 3".

**Drawer intercepting clicks while hidden.** Phase 11's investigation
drawer set `display: flex` unconditionally in CSS, which — per normal
cascade rules — overrides the browser's default `[hidden] { display: none
}` rule once an author style declares `display` on the same element. The
hidden drawer was therefore still laid out and intercepting clicks,
blocking the "Run Reconciliation" button underneath it. Caught by a
Playwright pass, fixed by scoping the rule to `.drawer:not([hidden])`. See
CLAUDE.md Phase 11 for the full writeup.
