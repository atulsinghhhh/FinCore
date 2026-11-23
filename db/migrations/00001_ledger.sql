-- +goose Up

-- Double-entry ledger.
--
-- Invariants enforced by the database (not just the application):
--   1. Every ledger transaction has >= 2 postings and debits = credits per currency
--      (deferred constraint triggers, checked at COMMIT).
--   2. Ledger transactions and postings are append-only (no UPDATE / DELETE / TRUNCATE).
--   3. A posting's currency must match its account's currency (composite FK).
--   4. Cached balances are maintained by trigger in the same DB transaction as the
--      postings, and an account that disallows negatives can never go below zero.
--   5. Money is stored as bigint minor units (paise), never floats.

CREATE TABLE accounts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code           text NOT NULL UNIQUE CHECK (code <> ''),
    name           text NOT NULL,
    type           text NOT NULL CHECK (type IN ('asset', 'liability', 'equity', 'revenue', 'expense')),
    normal_balance text NOT NULL GENERATED ALWAYS AS (
                       CASE WHEN type IN ('asset', 'expense') THEN 'debit' ELSE 'credit' END
                   ) STORED,
    currency       text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    allow_negative boolean NOT NULL DEFAULT false,
    metadata       jsonb NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, currency)
);

CREATE TABLE ledger_transactions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text NOT NULL UNIQUE CHECK (idempotency_key <> ''),
    request_hash    text NOT NULL,
    description     text NOT NULL,
    reverses_id     uuid UNIQUE REFERENCES ledger_transactions (id),
    metadata        jsonb NOT NULL DEFAULT '{}',
    effective_at    timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE postings (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id uuid NOT NULL REFERENCES ledger_transactions (id),
    account_id     uuid NOT NULL,
    currency       text NOT NULL,
    direction      text NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount         bigint NOT NULL CHECK (amount > 0),
    -- Copied from the parent transaction by trigger; denormalised for point-in-time queries.
    effective_at   timestamptz NOT NULL,
    FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency)
);

CREATE INDEX postings_transaction_id_idx ON postings (transaction_id);
CREATE INDEX postings_account_effective_idx ON postings (account_id, effective_at);

-- Cached projection of postings. Always rebuildable from the postings table.
CREATE TABLE account_balances (
    account_id     uuid PRIMARY KEY REFERENCES accounts (id),
    currency       text NOT NULL,
    normal_balance text NOT NULL,
    allow_negative boolean NOT NULL,
    debits_total   bigint NOT NULL DEFAULT 0 CHECK (debits_total >= 0),
    credits_total  bigint NOT NULL DEFAULT 0 CHECK (credits_total >= 0),
    balance        bigint NOT NULL GENERATED ALWAYS AS (
                       CASE WHEN normal_balance = 'debit'
                            THEN debits_total - credits_total
                            ELSE credits_total - debits_total
                       END
                   ) STORED,
    version        bigint NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_balances_non_negative CHECK (allow_negative OR balance >= 0)
);

-- +goose StatementBegin
CREATE FUNCTION accounts_create_balance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO account_balances (account_id, currency, normal_balance, allow_negative)
    VALUES (NEW.id, NEW.currency, NEW.normal_balance, NEW.allow_negative);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER accounts_create_balance
    AFTER INSERT ON accounts
    FOR EACH ROW EXECUTE FUNCTION accounts_create_balance();

-- +goose StatementBegin
CREATE FUNCTION accounts_guard_immutable_fields() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.code IS DISTINCT FROM OLD.code
       OR NEW.type IS DISTINCT FROM OLD.type
       OR NEW.currency IS DISTINCT FROM OLD.currency
       OR NEW.allow_negative IS DISTINCT FROM OLD.allow_negative THEN
        RAISE EXCEPTION 'account % code, type, currency and allow_negative are immutable', OLD.id
            USING ERRCODE = 'FC002';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER accounts_guard_immutable_fields
    BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION accounts_guard_immutable_fields();

-- +goose StatementBegin
CREATE FUNCTION ledger_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% on % is not allowed: the ledger is append-only', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'FC002';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ledger_transactions_append_only
    BEFORE UPDATE OR DELETE ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER ledger_transactions_no_truncate
    BEFORE TRUNCATE ON ledger_transactions
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER postings_append_only
    BEFORE UPDATE OR DELETE ON postings
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER postings_no_truncate
    BEFORE TRUNCATE ON postings
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_reject_mutation();

-- +goose StatementBegin
CREATE FUNCTION postings_before_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    SELECT effective_at INTO NEW.effective_at
    FROM ledger_transactions WHERE id = NEW.transaction_id;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER postings_before_insert
    BEFORE INSERT ON postings
    FOR EACH ROW EXECUTE FUNCTION postings_before_insert();

-- +goose StatementBegin
CREATE FUNCTION postings_apply_to_balance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE account_balances
    SET debits_total  = debits_total  + CASE WHEN NEW.direction = 'debit'  THEN NEW.amount ELSE 0 END,
        credits_total = credits_total + CASE WHEN NEW.direction = 'credit' THEN NEW.amount ELSE 0 END,
        version       = version + 1,
        updated_at    = now()
    WHERE account_id = NEW.account_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER postings_apply_to_balance
    AFTER INSERT ON postings
    FOR EACH ROW EXECUTE FUNCTION postings_apply_to_balance();

-- +goose StatementBegin
CREATE FUNCTION ledger_assert_balanced(txn_id uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    posting_count int;
    bad_currency  text;
BEGIN
    SELECT count(*) INTO posting_count FROM postings WHERE transaction_id = txn_id;
    IF posting_count < 2 THEN
        RAISE EXCEPTION 'ledger transaction % has % postings; at least 2 are required', txn_id, posting_count
            USING ERRCODE = 'FC001';
    END IF;

    SELECT currency INTO bad_currency
    FROM postings
    WHERE transaction_id = txn_id
    GROUP BY currency
    HAVING sum(CASE WHEN direction = 'debit' THEN amount ELSE -amount END) <> 0
    LIMIT 1;
    IF bad_currency IS NOT NULL THEN
        RAISE EXCEPTION 'ledger transaction % is unbalanced in %', txn_id, bad_currency
            USING ERRCODE = 'FC001';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION ledger_transactions_check_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM ledger_assert_balanced(NEW.id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION postings_check_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM ledger_assert_balanced(NEW.transaction_id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- Deferred: postings are inserted after their transaction row, so the check runs at COMMIT.
CREATE CONSTRAINT TRIGGER ledger_transactions_balanced
    AFTER INSERT ON ledger_transactions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger_transactions_check_balanced();

-- Also guards postings added later to an already-committed transaction.
CREATE CONSTRAINT TRIGGER postings_balanced
    AFTER INSERT ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION postings_check_balanced();

-- +goose Down
DROP TABLE account_balances;
DROP TABLE postings;
DROP TABLE ledger_transactions;
DROP TABLE accounts;
DROP FUNCTION ledger_transactions_check_balanced();
DROP FUNCTION postings_check_balanced();
DROP FUNCTION ledger_assert_balanced(uuid);
DROP FUNCTION postings_apply_to_balance();
DROP FUNCTION postings_before_insert();
DROP FUNCTION ledger_reject_mutation();
DROP FUNCTION accounts_guard_immutable_fields();
DROP FUNCTION accounts_create_balance();
