-- Shared schema used by Bot, Worker, and Dashboard.
-- Each service connects to the same Postgres database with its own pool.

CREATE TABLE IF NOT EXISTS tickets (
    id             BIGSERIAL PRIMARY KEY,
    guild_id       TEXT NOT NULL,
    channel_id     TEXT NOT NULL,
    user_id        TEXT NOT NULL,
    subject        TEXT NOT NULL,
    status         TEXT NOT NULL,                 -- open | closed
    transcript_url TEXT,
    opened_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS user_levels (
    guild_id   TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    xp         BIGINT NOT NULL DEFAULT 0,
    level      BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (guild_id, user_id)
);

CREATE TABLE IF NOT EXISTS mod_logs (
    id           BIGSERIAL PRIMARY KEY,
    guild_id     TEXT NOT NULL,
    moderator_id TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    action       TEXT NOT NULL,                   -- kick | ban | mute | warn
    reason       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS applications (
    id         BIGSERIAL PRIMARY KEY,
    guild_id   TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    role       TEXT NOT NULL,
    answers    JSONB NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending',   -- pending | accepted | rejected
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS application_forms (
    guild_id TEXT NOT NULL,
    role     TEXT NOT NULL,
    url      TEXT NOT NULL,
    PRIMARY KEY (guild_id, role)
);

CREATE TABLE IF NOT EXISTS giveaways (
    id           BIGSERIAL PRIMARY KEY,
    guild_id     TEXT NOT NULL,
    channel_id   TEXT NOT NULL,
    message_id   TEXT NOT NULL,
    prize        TEXT NOT NULL,
    winners      TEXT[],
    ends_at      TIMESTAMPTZ NOT NULL,
    status       TEXT NOT NULL,                    -- running | ended
    lock_channel BOOLEAN NOT NULL DEFAULT FALSE    -- channel locked while running?
);

-- Per-user entries for button-based giveaways. Bot is the writer; the
-- Worker reads this table when drawing winners.
CREATE TABLE IF NOT EXISTS giveaway_entries (
    giveaway_id BIGINT NOT NULL,
    user_id     TEXT NOT NULL,
    entered_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (giveaway_id, user_id)
);

CREATE INDEX IF NOT EXISTS giveaway_entries_user_idx ON giveaway_entries (user_id);
