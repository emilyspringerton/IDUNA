-- S153-07: okemily.com blog. blog.write gates POST /api/v1/blog/posts —
-- reading (list/get) is public, posting (programmatic or manual, same
-- endpoint) requires this permission. Granted to EMILY-PRIME in
-- config/agents.json — the agent Claude Code/Emily Prime posts as.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000033', 'blog.write', 'Publish posts to the okemily.com blog');
