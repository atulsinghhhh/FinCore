#!/usr/bin/env bash
# Seeds demo data through the public API (sandbox provider):
# two merchants, captured/refunded/declined/manual payments, payouts, and a
# settlement file with deliberate exceptions for the reconciliation screen.
#
#   ./scripts/seed.sh            (reads ADMIN_API_TOKEN from .env)
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; source .env; set +a
API=${API_URL:-http://localhost:8080}

j() { python3 -c "import sys,json; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }
admin() { curl -sf -X "$1" "$API$2" -H "Authorization: Bearer $ADMIN_API_TOKEN" -H "Idempotency-Key: $(uuidgen)" -H 'Content-Type: application/json' ${3:+-d "$3"}; }
as() { local key=$1; shift; curl -sf -X "$1" "$API$2" -H "Authorization: Bearer $key" -H "Idempotency-Key: $(uuidgen)" -H 'Content-Type: application/json' ${3:+-d "$3"}; }

merchant() {
  admin POST /v1/admin/merchants "{\"name\":\"$1\",\"currency\":\"INR\",\"fee_bps\":$2}"
}
pay() { as "$1" POST /v1/payments "{\"amount\":$2,\"currency\":\"INR\",\"payment_method\":\"${3:-pm_card_success}\",\"capture_method\":\"${4:-automatic}\",\"description\":\"${5:-Order}\"}"; }

echo "Creating merchants…"
CHAI=$(merchant "Chai Point" 200);   CHAI_KEY=$(echo "$CHAI" | j "d['api_key']['secret']")
BOOKS=$(merchant "Bookworm Books" 175); BOOKS_KEY=$(echo "$BOOKS" | j "d['api_key']['secret']")

echo "Payments…"
P1=$(pay "$CHAI_KEY" 125000 pm_card_success automatic "Masala chai — annual subscription" | j "d['id']")
P2=$(pay "$CHAI_KEY" 48000 pm_card_success automatic "Office tea order" | j "d['id']")
P3=$(pay "$CHAI_KEY" 9900 pm_card_success automatic "Single cutting chai" | j "d['id']")
pay "$CHAI_KEY" 15000 pm_card_declined automatic "Declined card" >/dev/null
pay "$CHAI_KEY" 22000 pm_card_insufficient_funds automatic "Insufficient funds" >/dev/null
B1=$(pay "$BOOKS_KEY" 249900 pm_card_success automatic "Box set — fantasy collection" | j "d['id']")
B2=$(pay "$BOOKS_KEY" 69900 pm_card_success manual "Pre-order (authorized, capture on dispatch)" | j "d['id']")
B3=$(pay "$BOOKS_KEY" 35000 pm_card_success automatic "Paperback" | j "d['id']")
for i in $(seq 1 6); do pay "$BOOKS_KEY" $((10000 + i * 3700)) >/dev/null; done

echo "Refunds…"
as "$CHAI_KEY" POST "/v1/payments/$P2/refunds" '{"amount":12000,"reason":"two items missing"}' >/dev/null
as "$BOOKS_KEY" POST "/v1/payments/$B3/refunds" '{"reason":"customer returned the book"}' >/dev/null

echo "Payouts…"
PO1=$(as "$CHAI_KEY" POST /v1/payouts '{"amount":100000}' | j "d['id']")
admin POST "/v1/admin/sandbox/payouts/$PO1/paid" >/dev/null
as "$BOOKS_KEY" POST /v1/payouts '{"amount":150000}' >/dev/null   # left pending

echo "Settlement file…"
pid() { curl -sf "$API/v1/admin/payments/$1" -H "Authorization: Bearer $ADMIN_API_TOKEN" | j "d['provider_payment_id']"; }
TOMORROW=$(python3 -c "import datetime; print((datetime.datetime.now(datetime.UTC)+datetime.timedelta(days=4)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
CSV=$(mktemp)
cat > "$CSV" <<CSV
settlement_id,entity_type,entity_id,amount,fee,tax,currency,settled_at
setl_demo_1,payment,$(pid "$P1"),125000,2500,450,INR,$TOMORROW
setl_demo_1,payment,$(pid "$B1"),249900,4998,900,INR,$TOMORROW
setl_demo_1,payment,$(pid "$P3"),9000,198,36,INR,$TOMORROW
setl_demo_1,payment,pay_SettledButUnknown,55500,1110,200,INR,$TOMORROW
setl_demo_1,adjustment,adj_chargeback_fee,1500,0,0,INR,$TOMORROW
CSV
curl -sf -X POST "$API/v1/admin/settlements?provider=sandbox&source=setl_demo_1.csv" \
  -H "Authorization: Bearer $ADMIN_API_TOKEN" -H 'Content-Type: text/csv' --data-binary "@$CSV" \
  | j "'reconciled: ' + str(d['summary']['matched']) + ' matched, ' + str(d['summary']['exceptions']) + ' exceptions'"
rm -f "$CSV"
echo "Done. Chai Point key: $CHAI_KEY"
