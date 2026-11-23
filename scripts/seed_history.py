#!/usr/bin/env python3
"""Demo history for the dashboard charts (TEST MODE ONLY).

Creates a few merchants with a realistic mix of payments through the public
API (successes, declines, refunds, pending authorisations), then spreads
payment creation dates across the last 90 days with an upward trend so the
overview charts have shape.

Backdating rewrites payments.created_at directly in the local database. The
ledger is append-only and keeps its real timestamps, so statements still
show when money actually moved. Never run this against real data.

    python3 scripts/seed_history.py
"""
import json, os, random, subprocess, sys, urllib.request, uuid

API = os.environ.get("API_URL", "http://localhost:8080")
env = dict(l.strip().split("=", 1) for l in open(".env") if "=" in l and not l.startswith("#"))
TOKEN = env["ADMIN_API_TOKEN"]

def call(method, path, token, body=None):
    req = urllib.request.Request(API + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": f"Bearer {token}", "Idempotency-Key": str(uuid.uuid4()), "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read() or b"null"), r.headers
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"null"), e.headers

status, health, headers = call("GET", "/healthz", TOKEN)
if headers.get("FinCore-Mode") != "test":
    sys.exit("refusing: this script only runs against a test-mode API")

random.seed(7)
merchants = [("Masala Mart", 175), ("Urban Threads", 220), ("QuickCab Rides", 150), ("Brew & Bean", 200)]
items = {
    "Masala Mart": ["Weekly groceries", "Spice box", "Basmati 5kg", "Festive hamper"],
    "Urban Threads": ["Kurta set", "Denim jacket", "Silk saree", "Sneakers"],
    "QuickCab Rides": ["Airport ride", "City ride", "Outstation trip", "Rental 4h"],
    "Brew & Bean": ["Cold brew", "Cappuccino x2", "Breakfast combo", "Coffee beans 250g"],
}
for name, fee in merchants:
    s, m, _ = call("POST", "/v1/admin/merchants", TOKEN, {"name": name, "currency": "INR", "fee_bps": fee})
    key = m["api_key"]["secret"]
    n_ok = n_fail = 0
    for _ in range(110):
        r = random.random()
        method = "pm_card_declined" if r < 0.07 else "pm_card_insufficient_funds" if r < 0.11 else "pm_card_success"
        capture = "manual" if random.random() < 0.04 else "automatic"
        amount = random.choice([random.randint(99, 999), random.randint(1000, 4999), random.randint(5000, 24999)]) * 100 // 100 * 100
        s, p, _ = call("POST", "/v1/payments", key, {"amount": amount, "currency": "INR", "payment_method": method,
                                                     "capture_method": capture, "description": random.choice(items[name])})
        if p and p.get("status") == "captured":
            n_ok += 1
            if random.random() < 0.08:
                call("POST", f"/v1/payments/{p['id']}/refunds", key, {"amount": amount // random.choice([1, 2, 4]), "reason": "customer request"})
        elif p and p.get("status") == "failed":
            n_fail += 1
    print(f"{name:16} {n_ok} captured, {n_fail} declined")

# Spread test-mode payments over 90 days, denser towards today.
sql = """
UPDATE payments
SET created_at = now() - (89 * (1 - sqrt(random()))) * interval '1 day' - random() * interval '20 hours',
    updated_at = now()
WHERE created_at > now() - interval '1 day';
"""
out = subprocess.run(["docker", "exec", "-i", "fincore-postgres", "psql", "-U", env.get("POSTGRES_USER", "fincore"), "-d", env.get("POSTGRES_DB", "fincore"), "-tAc", sql],
                     capture_output=True, text=True)
print("backdated:", out.stdout.strip() or out.stderr.strip())
