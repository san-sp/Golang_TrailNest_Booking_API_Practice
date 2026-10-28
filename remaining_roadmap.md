# TrailNest Booking API --- Current Project Status & Remaining Roadmap

**Repository:** `san-sp/Golang_TrailNest_Booking_API_Practice`\
**Branch:** `main`\
**Latest inspected commit:** `c768e8b` ---
`fix: persist activity uploads in Docker`\
**Stack:** Go, Gin, PostgreSQL 17, pgx/v5, Docker Compose, Razorpay

> Status rule: implemented means the code exists; verified means the
> relevant test or operational check has actually run. Audit first and
> implement only gaps supported by evidence.

## 1. Latest progress

### Commits pushed to `main`

- [x] `8d461cc` --- `feat: handle activity cancellation and refunds`
- [x] `3832eb7` --- `feat: start refund recovery worker`
- [x] `c768e8b` --- `fix: persist activity uploads in Docker`

### Latest reported test result

The latest reported `go test ./... -count=1` passed every package with
tests: `internal/activity`, `internal/auth`, `internal/booking`,
`internal/http/middleware`, `internal/payment`, `internal/reservation`,
and `internal/storage`.

Several packages report `[no test files]`, including
`internal/cancellation`; cancellation behavior also has integration
coverage in other packages. Do not infer the feature is untested just
because its package has no standalone test file.

**Not yet confirmed by the latest evidence:** Docker image build,
clean-database migration run, `go test -race ./...`, and live Razorpay
end-to-end refund recovery.

## 2. Implemented features

### 2.1 Foundation and infrastructure

- [x] Go modular structure using `cmd/` and `internal/`; Gin and
      PostgreSQL via pgx/v5.
- [x] Environment configuration, `.env.example`, structured JSON
      logging (`log/slog`).
- [x] Request ID, request logging, panic recovery, standardized JSON
      errors.
- [x] Request-body limits, HTTP rate limiting, CORS.
- [x] HTTP server timeouts and graceful shutdown.
- [x] `/healthz` and `/readyz` with PostgreSQL readiness check.
- [x] Dockerfile, Docker Compose, PostgreSQL 17, persistent database
      volume.
- [x] API host port `8081`, PostgreSQL host port `5436`.
- [x] Versioned up/down SQL migrations, `.gitignore`, `.dockerignore`,
      incremental Git/GitHub workflow.
- [x] `/backups/` excluded from Git.
- [x] Named `trailnest_uploads` volume mounted at `/app/uploads`;
      Dockerfile creates the upload directory and assigns ownership to the
      non-root app user. Build/runtime validation remains pending.

### 2.2 Authentication and users

- [x] Registration, password hashing/verification, JWT access tokens
      and authentication middleware.
- [x] Operator/admin role authorization; customer identity from
      authenticated server context.
- [x] Email normalization, strict JSON decoding, body limits, input
      validation.
- [x] Email verification, resend endpoint, email-provider integration.
- [x] Login and verification-resend rate limiting.
- [x] Current-user endpoint; administrator user listing/retrieval and
      operator promotion.
- [x] Bootstrap commands and auth/token tests.

### 2.3 Activities and image uploads

- [x] Public activity listing/details; operator
      create/list/detail/update; administrator management.
- [x] Activity status transitions and fields for owner, description,
      location, schedule, capacity, price, currency, and image URL.
- [x] Input validation, public search/location filtering, validated
      page/limit parameters.
- [x] Image upload/local storage, JPEG/PNG content validation,
      size/dimension limits, resizing and WebP processing.
- [x] Image storage, HTTP, handler, repository, and integration tests.
- [x] Activity cancellation delegates to
      `internal/cancellation.Service`.

### 2.4 Bookings and capacity

- [x] Customer create/list/detail/cancel endpoints; administrator
      booking list/detail.
- [x] Quantity validation; block booking past/unpublished activities
      and direct booking of paid activities.
- [x] Transactional booking creation and activity row lock
      (`SELECT ... FOR UPDATE`).
- [x] Confirmed-booking capacity accounting, insufficient-capacity
      conflict handling, rollback.
- [x] Ownership and repository integration tests.
- [x] Existing HTTP concurrency test launches 50 simultaneous requests
      for one remaining spot; asserts one success, 49 conflicts, and final
      database state. Do not create a duplicate test.

### 2.5 Reservations

- [x] Reservation creation, persistence, lifecycle states and expiry
      timestamp.
- [x] Bounded expiry batches, repeated processing of overdue work,
      worker context cancellation and error logging.
- [x] Expiry worker integrated with application shutdown.
- [x] Administrator list/detail endpoints, repository tests, separate
      expiry-test database support.

### 2.6 Payments, webhooks, reconciliation and refunds

- [x] Razorpay checkout, payment verification/signature verification,
      webhook endpoint.
- [x] Success/failure webhook handling, provider order/payment ID
      validation, payment attempts and hosted payment links.
- [x] Payment-to-reservation association and booking fulfillment after
      successful payment.
- [x] Webhook event persistence/idempotency, unsupported event
      rejection, duplicate event handling.
- [x] Different successful event IDs for the same payment do not
      create another booking; concurrent duplicate failed-webhook
      coverage.
- [x] Admin reconciliation, provider-side payment
      retrieval/validation, captured-payment handling after reservation
      expiry.
- [x] `refund_required` fulfillment state, durable refund jobs, refund
      worker, provider response/reconciliation logic.
- [x] Stale refund recovery code and `RunRefundRecoveryWorker` wired
      into API startup with shared shutdown context and wait group.
- [x] Recovery worker cancellation/configuration tests.
- [x] Activity cancellation creates durable refund jobs for eligible
      paid bookings; provider refund processing is asynchronous.
- [x] Cancellation/webhook consistency integration tests.

### 2.7 Wishlists and database

- [x] Add/list/remove customer wishlist entries.
- [x] Core users/activities/bookings schema, email verification,
      wishlist, activity search indexes.
- [x] Reservation/payment tables, pricing, webhook events, fulfillment
      status, provider order IDs, payment attempts/links.
- [x] Booking-reservation association, refund tables/status, activity
      image URL.
- [x] Fifteen versioned migration pairs were present in the inspected
      repository.

## 3. Remaining work --- audit first

### Priority 1: Booking, cancellation and capacity correctness

- [ ] Audit booking state transitions and cancellation rules.
- [ ] Verify booking cancellation releases capacity at most once;
      repeated cancellation behaves correctly.
- [ ] Verify activity cancellation prevents new bookings and handles
      pending reservations, confirmed bookings, and eligible refunds
      consistently.
- [ ] Repeat concurrent cancellation/webhook tests and verify final
      database state.
- [ ] Check capacity reductions cannot invalidate confirmed bookings.
- [ ] Audit transaction rollback and commit-error behavior.
- [ ] Re-run the existing 50-request concurrency test repeatedly
      against the correct disposable database.
- [ ] Confirm whether operators need a route to view bookings for
      their own activities. The inspected router had customer and
      administrator booking routes, but no dedicated operator
      activity-booking route. Implement only if required.
- [ ] Fix only gaps discovered by the audit.

### Priority 2: Payment and refund reliability

- [ ] Audit payment/refund state transitions.
- [ ] Test duplicate success/failure webhooks and different event IDs
      reporting the same payment.
- [ ] Test mismatched event types, payment IDs, order IDs, amounts and
      currencies.
- [ ] Test provider timeouts/transient errors.
- [ ] Test repeated/concurrent reconciliation; ensure it cannot create
      another charge or booking.
- [ ] Verify a stale `processing` refund recovers safely after a
      crash.
- [ ] Verify ambiguous provider outcomes never trigger a blind
      duplicate refund.
- [ ] Test idempotent refund retries and mismatched provider refund
      records.
- [ ] Complete Razorpay Test Mode checkout, webhook, reconciliation
      and refund verification.
- [ ] Document how to inspect and recover failed refund jobs.

### Priority 3: Activity API and security audit

- [ ] Review pagination, out-of-range pages, deterministic ordering
      and whether total-count metadata is needed.
- [ ] Decide whether date filters are required; do not add them
      without a requirement.
- [ ] Confirm parameterized SQL for filter values and allowlisted sort
      fields where applicable.
- [ ] Verify operator ownership and administrator permissions across
      activity endpoints.
- [ ] Audit malformed/expired JWTs and intended `401` versus `403`
      responses.
- [ ] Test role/ownership allow/deny cases across protected routes.
- [ ] Review account-enumeration risks in login responses.
- [ ] Ensure secrets never appear in logs/errors.
- [ ] Review production CORS, required configuration validation,
      rate-limiter limitations, timezone handling, TLS and secret
      management.

### Priority 4: Docker, database and tests

- [ ] Run `docker compose config -q`.
- [ ] Run `docker compose build api`; fix any failure.
- [ ] Verify Docker Compose startup, readiness, shutdown and
      upload-volume persistence.
- [ ] Test failed-upload cleanup.
- [ ] Verify migrations initialize a clean database from scratch.
- [ ] Run the full test script with `trailnest_test` and
      `trailnest_expiry_test` configured separately.
- [x] `go test ./... -count=1` passed after
      cancellation/refund-recovery changes in the latest reported run.
- [ ] Run `go test -race ./...` where practical and record the result.
- [ ] Confirm tests do not depend on leftover data.
- [ ] Ensure `.env`, `.env.docker`, credentials and provider secrets
      are not committed.
- [ ] Review runtime permissions and production configuration.

### Priority 5: Documentation

The inspected GitHub tree did not contain a `README.md`, `docs/`
directory or GitHub Actions workflow. - \[ \] Create `README.md`:
overview, prerequisites, env vars, setup, migrations, run/test
instructions and API examples. - \[ \] Create `docs/architecture.md`:
packages, dependency direction and key request flows. - \[ \] Create
`docs/openapi.yaml`: routes, schemas, authentication, roles, errors and
examples. - \[ \] Create `docs/BUILD_PROGRESS.md`: verified milestones,
tests, decisions, known issues and next task. - \[ \] Document
role/permission matrix, cancellation rules, payment/refund states, test
DB setup and test runner. - \[ \] Document image storage/volume
persistence, health/readiness, backup/restore, worker troubleshooting
and refund recovery. - \[ \] Update `extended_roadmap.md` to match
actual implementation.

### Priority 6: CI and operational readiness

- [ ] Add GitHub Actions for formatting, `go vet ./...` and Go tests.
- [ ] Verify `gofmt`, run `go vet ./...`, and run race checks where
      practical.
- [ ] Validate clean database/migrations in CI where practical; never
      use production credentials.
- [ ] Review log context, request IDs, connection-pool sizing and
      timeout behavior.
- [ ] Add targeted DB benchmarks and realistic concurrent HTTP load
      tests only after correctness is established.

## 4. Deferred enhancements --- not blockers

- [ ] Transactional outbox for reliable booking notifications.
- [ ] Async notification worker and booking confirmation/cancellation
      emails.
- [ ] Durable notification retries and idempotent consumers.
- [ ] Multiple activity photos/gallery management.
- [ ] Object storage if deployment requirements justify it.
- [ ] Additional performance/load-testing infrastructure.

**Scope rule:** Do not add Redis, RabbitMQ, Kafka, microservices,
Kubernetes, Terraform or a frontend just to make the project look more
advanced. The target is a well-tested Go modular monolith using Gin and
PostgreSQL. Add infrastructure only when a demonstrated requirement
justifies it.

## 5. Final acceptance checklist

- [ ] Clean database initializes through documented migrations.
- [ ] App starts locally and through Docker Compose.
- [ ] Authentication, email verification, roles and ownership checks
      behave correctly.
- [ ] Activity create/discovery/update/publish/cancel/upload flows
      work.
- [ ] Booking creation/cancellation preserve capacity invariants.
- [ ] Concurrent requests cannot oversell activities.
- [ ] Reservation expiry remains correct under concurrent
      payment/cancellation.
- [ ] Webhooks and reconciliation are idempotent.
- [ ] Refund processing and crash recovery are safely verified.
- [ ] Errors are consistent/safe and logs useful.
- [ ] Focused tests and full uncached suite pass.
- [ ] Race checks pass where practical and results are recorded.
- [ ] README, architecture docs, OpenAPI and progress ledger match the
      code.
- [ ] Git history is coherent; no secrets are committed.
- [ ] Known limitations and recovery procedures are documented.
- [ ] I can explain important code paths, SQL transactions, locks,
      state machines and failure handling in an interview.

## 6. Next task

**Start with the booking/cancellation/capacity correctness audit.**
Inspect existing repositories and tests first. Mark each acceptance item
as **verified**, **incomplete** or **failing** based on evidence. The
50-request concurrency test already exists; do not duplicate it.
Implement only gaps found.

Then finish payment/refund verification, validate Docker and clean
migrations, write documentation, add CI, and run final checks.

**Working rule:** One bounded change at a time. Run relevant tests,
inspect the diff, commit only verified changes, and update this roadmap
when evidence changes.
