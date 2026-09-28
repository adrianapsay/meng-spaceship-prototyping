-- +goose Up
CREATE TABLE designs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    prompt     text NOT NULL,
    provider   text NOT NULL,
    model      text NOT NULL,
    status     text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'passed', 'failed')),
    summary    text,
    error      text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE iterations (
    design_id  uuid NOT NULL REFERENCES designs (id) ON DELETE CASCADE,
    n          integer NOT NULL,
    spec       jsonb NOT NULL,
    report     jsonb NOT NULL,
    passed     boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (design_id, n)
);

-- Append-only event log per design; the SSE endpoint replays it.
CREATE TABLE events (
    id         bigserial PRIMARY KEY,
    design_id  uuid NOT NULL REFERENCES designs (id) ON DELETE CASCADE,
    type       text NOT NULL,
    data       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_design_id_id ON events (design_id, id);

-- +goose Down
DROP TABLE events;
DROP TABLE iterations;
DROP TABLE designs;
