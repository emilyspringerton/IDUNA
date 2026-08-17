-- Prompt-o-verse gallery on okemily.com. promptoverse.write gates
-- POST /api/v1/promptoverse/nodes -- reading (list/get) is public, same
-- shape as tyler.write (202608060001_tyler_permissions.sql). Granted to
-- EMILY-PRIME in config/agents.json -- the agent Claude Code/Emily Prime
-- posts as.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000039', 'promptoverse.write', 'Publish nodes to the Prompt-o-verse gallery on okemily.com');
