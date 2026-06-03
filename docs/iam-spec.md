# FARTHQ Core Architecture Specification

## Document ID: HQ-SPEC-IAM-094

## System Title: IDUNA Platform IAM & Governance Service (The Central Trust Authority)

## Status: APPROVED / ARCHIVAL-READY

---

### 1. Vision & Architectural Pivot

Historically, **IDUNA** operated as a perimeter game-specific registration and profiling database. This specification establishes its absolute pivot into a foundational, ecosystem-wide **Platform Identity and Access Management (IAM) and Governance Service** serving all current and future FARTHQ consumer domains (including `FATBABY`, `KIKORYU`, `SECWATCH`, and autonomous agents like `EMILY`).

```
 +------------------------+
 |  Google OAuth Provider |
 +-----------+------------+
             |
             | Identity Assertion (ID Token)
             v
+-----------------------------------------------------------+
|                        IDUNA IAM                          |
|  - External Identity Resolution                           |
|  - Relational RBAC "True Store" Enforcer                  |
|  - Event-Sourced Audit Ledger                             |
|  - JWT Issuing Authority (Private Key Signer)            |
+----------------------------+------------------------------+
                             |
                             | IDUNA-Issued JWT
                             +-----------------------+-----------------------+
                             |                       |                       |
                             v                       v                       v
                      +--------------+        +--------------+        +--------------+
                      |   FATBABY    |        |   SECWATCH   |        |   KIKORYU    |
                      | Consumer Svc |        | Consumer Svc |        | Consumer Svc |
                      +--------------+        +--------------+        +--------------+
                       (Verifies PubKey)       (Verifies PubKey)       (Verifies PubKey)

```

#### 1.1 The Single Source of Truth

Downstream consumer domains must completely decouple from external OAuth providers. IDUNA is the sole authority qualified to validate external assertions, translate them into localized relational identities, inject organizational capabilities, and sign a secondary ecosystem-level bearer token.

#### 1.2 Administrative Aesthetics (The "Back Office" / Aunt Sally)

The administration interface is explicitly stripped of modern consumer dashboards, gamified charts, and vibrant gradients. It must present a highly structured, transactional, clerical environment resembling corporate mainframe terminal emulators, physical microfiche indexing, and ledger bookkeeping.

* **Aesthetic Anchors**: Hard borders, high data-density layouts, clear archival row structures, monospace type treatments, and strict tabular pagination.
* **Color Hierarchy**: Neutral, muted institutional backdrops (slate, bone, off-black, cold greys).
* **The Crimson Core (Rose Gold Accent)**: A precise, highly restricted color token (`#B76E79` / Rose Gold) is reserved strictly for irreversible consent points—such as permanent account banishment, human operator suspension, or autonomous agent de-authorization.

---

### 2. Core Data Model (The "True Store")

The relational database layer serves as the deterministic status state engine of record. It enforces classic Hierarchical Role-Based Access Control (RBAC) along with strict isolation barriers for software-driven autonomous actors.

```sql
-- IDUNA Platform IAM Schema Initialization
-- Targeted Engine: MySQL 8.0+ / InnoDB

SET FOREIGN_KEY_CHECKS = 0;

-- ---------------------------------------------------------------------
-- Table: users
-- Core human identity matrix
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `users` (
    `id` VARCHAR(36) NOT NULL,
    `email` VARCHAR(255) NOT NULL,
    `google_subject` VARCHAR(255) NOT NULL,
    `gamertag` VARCHAR(64) DEFAULT NULL,
    `status` ENUM('ACTIVE', 'SUSPENDED', 'BANNED', 'PENDING') NOT NULL DEFAULT 'PENDING',
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_users_email` (`email`),
    UNIQUE KEY `uk_users_google_subject` (`google_subject`),
    UNIQUE KEY `uk_users_gamertag` (`gamertag`),
    INDEX `idx_users_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------
-- Table: roles
-- Archetypal assignment structures
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `roles` (
    `id` VARCHAR(36) NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `description` VARCHAR(255) DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_roles_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------
-- Table: permissions
-- Fine-grained programmatic capability nodes
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `permissions` (
    `id` VARCHAR(36) NOT NULL,
    `name` VARCHAR(128) NOT NULL, -- Format: 'domain.action' e.g. 'fatbaby.read'
    `description` VARCHAR(255) DEFAULT NULL,
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_permissions_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------
-- Table: user_roles
-- Mapping join table linking human operators to functional profiles
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `user_roles` (
    `user_id` VARCHAR(36) NOT NULL,
    `role_id` VARCHAR(36) NOT NULL,
    `assigned_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`user_id`, `role_id`),
    CONSTRAINT `fk_ur_user_id` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_ur_role_id` FOREIGN KEY (`role_id`) REFERENCES `roles` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------
-- Table: role_permissions
-- Mapping join table declaring capabilities inherited by roles
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `role_permissions` (
    `role_id` VARCHAR(36) NOT NULL,
    `permission_id` VARCHAR(36) NOT NULL,
    `granted_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`role_id`, `permission_id`),
    CONSTRAINT `fk_rp_role_id` FOREIGN KEY (`role_id`) REFERENCES `roles` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_rp_permission_id` FOREIGN KEY (`permission_id`) REFERENCES `permissions` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------
-- Table: agents
-- First-class autonomous operational entities
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `agents` (
    `id` VARCHAR(36) NOT NULL,
    `owner_user_id` VARCHAR(36) NOT NULL, -- Human entity legally/operationally accountable
    `name` VARCHAR(128) NOT NULL,          -- e.g. 'EMILY', 'SECWATCH_BOT_01'
    `type` VARCHAR(64) NOT NULL,           -- e.g. 'CRON', 'LLM_AGENT', 'DAEMON'
    `status` ENUM('ACTIVE', 'SUSPENDED', 'DECOMMISSIONED') NOT NULL DEFAULT 'ACTIVE',
    `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_agents_name` (`name`),
    INDEX `idx_agents_status` (`status`),
    CONSTRAINT `fk_agents_owner` FOREIGN KEY (`owner_user_id`) REFERENCES `users` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------
-- Table: agent_permissions
-- Direct, explicit capabilities assigned exclusively to autonomous actors
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `agent_permissions` (
    `agent_id` VARCHAR(36) NOT NULL,
    `permission_id` VARCHAR(36) NOT NULL,
    `granted_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`agent_id`, `permission_id`),
    CONSTRAINT `fk_ap_agent_id` FOREIGN KEY (`agent_id`) REFERENCES `agents` (`id`) ON DELETE CASCADE,
    CONSTRAINT `fk_ap_permission_id` FOREIGN KEY (`permission_id`) REFERENCES `permissions` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET FOREIGN_KEY_CHECKS = 1;

```

---

### 3. Authentication & Authorization Flow

The execution chain operates deterministically. To secure the boundary, the signature algorithm must utilize **RS256** (RSA Signature with SHA-256) or **ES256** (ECDSA using P-256 and SHA-256).

```
[Client App]       [Google IDP]       [IDUNA IAM]        [Downstream Service]
     |                  |                  |                      |
     |--- 1. Login ---->|                  |                      |
     |<-- 2. ID Token --|                  |                      |
     |                                     |                      |
     |-------- 3. POST /auth/google ------>|                      |
     |           (Provides ID Token)       |                      |
     |                                     |                      |
     |                                     |-- 4. Verify Token    |
     |                                     |-- 5. Map Identity    |
     |                                     |-- 6. Collect RBAC    |
     |                                     |-- 7. Sign Identity   |
     |<------- 8. Return IDUNA JWT --------|                      |
     |                                                            |
     |------------------- 9. API Request with JWT --------------->|
     |                                                            |-- 10. Verify Signature
     |                                                            |-- 11. Authorize Capability
     |<------------------ 12. Payload / Execution ----------------|

```

#### 3.1 Token Transformation Engineering

Upon receiving a verified assertion payload from Google containing `sub`, IDUNA checks its identity lookup table.

##### Ingested Claims (From Google)

```json
{
  "iss": "https://accounts.google.com",
  "sub": "google-oauth2|109283019823091823091",
  "email": "operator@farthq.com",
  "email_verified": true
}

```

##### Output Ecosystem Claims (Signed by IDUNA)

```json
{
  "iss": "https://iam.farthq.internal",
  "sub": "usr_94f8ba11-37d4-4bbd-986c-0e2f9d6c7581",
  "aud": "farthq-ecosystem",
  "exp": 1782782400,
  "iat": 1782778800,
  "gamertag": "AUNT_SALLY_OPSC",
  "roles": [
    "ANALYST",
    "OPERATOR"
  ],
  "permissions": [
    "fatbaby.read",
    "secwatch.execute",
    "secwatch.read",
    "iduna.me.read"
  ]
}

```

#### 3.2 Downstream Verification Guard

Downstream architectures (e.g., `secwatch`) process authorization via stateless middleware layers.

1. **Cryptographic Validation**: Parse token from `Authorization: Bearer <JWT>`. Validate expirations (`exp`, `nbf`) and signatures against IDUNA's public key cluster (`/.well-known/jwks.json`).
2. **Permission Check**: Confirm if the specific execution context requires a permission node (e.g., `secwatch.execute`) and ensure it exists inside the flattened `permissions` array string match.

---

### 4. Event Sourcing Requirements

To maintain auditability within the ecosystem, IDUNA must write every critical state transition sequentially to a structured append-only log ledger before or atomically with the True Store commitment.

#### 4.1 Schema Definition for the Event Log Ledger

```sql
CREATE TABLE IF NOT EXISTS `iam_event_stream` (
    `event_id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `event_type` VARCHAR(128) NOT NULL,
    `aggregate_type` ENUM('USER', 'ROLE', 'PERMISSION', 'AGENT') NOT NULL,
    `aggregate_id` VARCHAR(36) NOT NULL,
    `operator_id` VARCHAR(36) NOT NULL, -- Human or Agent ID that authorized this change
    `payload` JSON NOT NULL,
    `recorded_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`event_id`),
    INDEX `idx_stream_aggregate` (`aggregate_type`, `aggregate_id`),
    INDEX `idx_stream_event_type` (`event_type`),
    INDEX `idx_stream_recorded` (`recorded_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

```

#### 4.2 Canonically Enforced Event Contract Specifications

##### Identity Events

* **`UserCreated`**: Executed during initial user provisioning pipeline sync.
* **`UserSuspended`**: Immediate lockout. Revokes downstream token validity.
* **`UserActivated`**: Restoration of access permissions.

##### RBAC Events

* **`RoleAssigned`** / **`RoleRevoked`**: Modifies user profiles.
* **`PermissionGranted`** / **`PermissionRevoked`**: Direct alteration of functional definitions.

##### Governance Events

* **`AgentCreated`**: Establishes tracking for an automated programmatic entity.
* **`AgentSuspended`**: Shuts down agent token workflows immediately.
* **`AuthorityActionExecuted`**: Logs critical administrative steps, such as bypassing checks or running overrides.

##### Payload Contract Schema Example (`AgentSuspended`)

```json
{
  "agent_id": "age_e3c544fa-166c-4824-916c-e4070a30b809",
  "reason": "Anomaly detected in continuous processing pipeline loops.",
  "remediation_ticket": "HQ-INC-9910",
  "previous_status": "ACTIVE",
  "new_status": "SUSPENDED"
}

```

---

### 5. Administrative UI ("The Back Office")

The interface is modeled as a functional terminal window rather than a contemporary dashboard canvas. It must adhere to a strict design system optimized for clarity, speed, and accuracy.

#### 5.1 Design Constraints

* **Typography**: Monospaced font families (`Courier New`, `SF Mono`, `JetBrains Mono`). Max 3 dynamic sizes (Header, Label, Data Row).
* **Color Palette**:
* Canvas: Dark Slate (`#1E222A`) or Ledger Bone (`#F5F5F0`)
* Data Ink: Off-white (`#E6E6E6`) or Dark Ink (`#111111`)
* Primary Accent: Institutional Grey Blue (`#4A5568`)
* Execution Warning / Confirmation (The Rose Gold Anchor): `#B76E79`



#### 5.2 User Interface Functional Matrix

* **User Management Ledger**: Displays plain text datagrs without interactive avatar or status elements. Provides single-action toggle keys. Suspension confirmation modals require typing the target user ID, with the execute button styled in `#B76E79`.
* **Agent Registry Matrix**: Lists automated background processes, showing their execution limits and direct permission controls. Includes real-time toggle kill switches for each daemon thread.
* **Audit Stream Terminal Output**: A continuous stream from the `iam_event_stream` ledger. Includes field filter strings for `operator_id`, `aggregate_id`, and `event_type`.

---

### 6. Implementation Checklist for Claude Code

The following implementation objectives are complete in-repository as of 2026-06-03. No placeholder files remain in the core IAM path:

* [x] **Database Setup**: Execute the relational schema for users, roles, permissions, agents, join tables, and the `iam_event_stream` table.
* [x] **Endpoint Refactoring**: Update `/api/v1/auth/google` to accept external ID tokens, match identities in the local database, and check their status values.
* [x] **Ecosystem JWT Processor**: Build an internal signer module that maps roles and sets permissions arrays using ES256 keys.
* [x] **API Security Middleware**: Add access controls to IDUNA's routes to restrict configuration changes to users with `iduna.admin` permission and protect capability-specific APIs with permission claims.
* [x] **Log Integration**: Connect event triggers to business functions to write entry records to the event stream during identity, RBAC, governance, and Apple publication updates.
* [x] **Unified Identity Target**: Update `/api/v1/identities/me` to return the complete access layout for current active accounts.

---

### 7. Core Route Declarations & API Layout

#### 7.1 Identity Verification Engine

* **URL**: `/api/v1/auth/google`
* **Method**: `POST`
* **Access**: Open / Pre-authentication

##### Expected Header Context

```http
Content-Type: application/json

```

##### Expected Payload Data

```json
{
  "id_token": "eyJhbGciOiJSUzI1NiIsImtpZCI6..."
}

```

##### Success Response (`200 OK`)

```json
{
  "success": true,
  "token_type": "Bearer",
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6ImlkdW5hLWtleS0wMSJ9...",
  "expires_in": 3600
}

```

##### Failure Response (`403 Forbidden`)

```json
{
  "error": "IDENTITY_SUSPENDED",
  "message": "This operational matrix identity has been suspended by administration board governance.",
  "reference_event": "evt_88391029301923"
}

```

#### 7.2 Core Account Entitlements Profile

* **URL**: `/api/v1/identities/me`
* **Method**: `GET`
* **Access**: Validated Ecosystem Bearer Token Required

##### Success Response (`200 OK`)

```json
{
  "identity": {
    "id": "usr_94f8ba11-37d4-4bbd-986c-0e2f9d6c7581",
    "email": "operator@farthq.com",
    "gamertag": "AUNT_SALLY_OPSC",
    "status": "ACTIVE"
  },
  "rbac": {
    "assigned_roles": ["ANALYST", "OPERATOR"],
    "effective_permissions": [
      "fatbaby.read",
      "secwatch.execute",
      "secwatch.read",
      "iduna.me.read"
    ]
  },
  "meta": {
    "server_epoch": 1782778805,
    "authority_signature_cluster": "https://iam.farthq.internal/.well-known/jwks.json"
  }
}

```

