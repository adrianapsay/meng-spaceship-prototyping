-- name: CreateDesign :one
INSERT INTO designs (prompt, provider, model)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetDesign :one
SELECT * FROM designs WHERE id = $1;

-- name: ListDesigns :many
SELECT d.*, count(i.n)::int AS iteration_count
FROM designs d
LEFT JOIN iterations i ON i.design_id = d.id
GROUP BY d.id
ORDER BY d.created_at DESC
LIMIT $1;

-- name: FinishDesign :exec
UPDATE designs
SET status = $2, summary = $3, error = $4, updated_at = now()
WHERE id = $1;

-- name: FailInterruptedDesigns :execrows
UPDATE designs
SET status = 'failed', error = 'interrupted: server restarted', updated_at = now()
WHERE status = 'running';

-- name: CreateIteration :exec
INSERT INTO iterations (design_id, n, spec, report, passed)
VALUES ($1, $2, $3, $4, $5);

-- name: ListIterations :many
SELECT * FROM iterations WHERE design_id = $1 ORDER BY n;

-- name: InsertEvent :one
INSERT INTO events (design_id, type, data)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListEventsAfter :many
SELECT * FROM events
WHERE design_id = $1 AND id > $2
ORDER BY id;
