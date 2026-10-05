#!/usr/bin/env bash
set -euo pipefail

: "${HERALD_TEST_DATABASE_URL:?Set HERALD_TEST_DATABASE_URL to a disposable PostgreSQL database}"

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
artifact_dir=${1:-"$repo_dir/artifacts/e2e"}
artifact="$artifact_dir/delivery-errors.json"

mkdir -p "$artifact_dir"
cd "$repo_dir"
go test -count=1 -json ./internal/fcm ./internal/webpush ./internal/server \
  -run '^(TestDispatchSendsHighPriorityDeliveryToFID|TestDispatchErrorVisibilityEndToEnd|TestTransportFailureRetriesUntilAttemptsAreSpent|TestPermanentRejectionFailsImmediately|TestAdminMessageHistoryEndToEnd)$' \
  | tee "$artifact"
echo "Delivery error E2E artifact: $artifact"
