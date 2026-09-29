-- QR-code Back Office login (founder real-time, 2026-09-29: "i dont have the password manager
-- set up i need a page in iDUNA admin that lets me scan a QR code to log in ... additional qr
-- code passkey functionality (make there a button to create a login code just like the nock
-- video uploader)"). Same real mint-a-token/render-a-QR shape as qr_codes/nock_video_upload_links,
-- reused for a materially different action (approve a login, not redirect/upload). See
-- handlers/admin_qr_login.go's own header comment for the full design, especially why the token
-- itself is deliberately never a bearer credential.
CREATE TABLE IF NOT EXISTS admin_qr_login_codes (
    token         VARCHAR(64)  PRIMARY KEY,
    status        VARCHAR(20)  NOT NULL DEFAULT 'pending', -- pending | approved | consumed | denied
    agent_name    VARCHAR(200) NOT NULL DEFAULT '', -- set on approve, for operator visibility only
    session_token TEXT         NOT NULL DEFAULT '', -- the real signed iduna_session JWT, minted at
                                                      -- approve time (the approving request already
                                                      -- carries full claims) and handed to the
                                                      -- waiting browser exactly once, then cleared
    created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    DATETIME     NOT NULL,
    approved_at   DATETIME
);
