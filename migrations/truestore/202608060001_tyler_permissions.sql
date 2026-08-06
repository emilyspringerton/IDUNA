-- TYLER reading room on okemily.com. tyler.write gates
-- POST /api/v1/tyler/episodes -- reading (list/get) is public, same shape
-- as 202607180001_blog_permissions.sql. Granted to EMILY-PRIME in
-- config/agents.json -- the agent Claude Code/Emily Prime posts as.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000038', 'tyler.write', 'Publish episodes to the TYLER reading room on okemily.com');
