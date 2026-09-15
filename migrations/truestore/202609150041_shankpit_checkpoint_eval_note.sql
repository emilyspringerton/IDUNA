-- S459-63, founder real-time: "how the fuck is my colab log gonna help it just says training" --
-- the diagnostics S459-61 added (real kill counts / crash output per evaluation match) only ever
-- reach the training process's own stdout, which needs the user to find and paste the right
-- lines out of a live Colab session. Real, better fix: have the training pipeline push its own
-- per-generation evaluation outcome as plain text alongside the checkpoint it already uploads, so
-- it's visible directly through the SAME registry API this session already queries -- no log
-- access needed at all. eval_note is real, optional (empty for generation 0's own real "nothing
-- to evaluate against yet" case, and for any pre-S459-63 row), and purely diagnostic -- it does
-- not feed back into the real elo column or any other real behavior.
ALTER TABLE shankpit_rl_checkpoints ADD COLUMN eval_note VARCHAR(500) NOT NULL DEFAULT '';
