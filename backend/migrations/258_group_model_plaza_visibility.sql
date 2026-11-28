-- Extension table storing whether an exclusive group (e.g. a subscription group)
-- should still be showcased publicly in Model Plaza while keeping is_exclusive=true
-- for API key binding and gateway access control.
-- Stored in a dedicated sidecar table to avoid modifying upstream groups table / ent schema.

CREATE TABLE IF NOT EXISTS group_model_plaza_visibility (
    group_id            BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    show_in_model_plaza BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
