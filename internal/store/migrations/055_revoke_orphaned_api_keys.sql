-- 055_revoke_orphaned_api_keys.sql
--
-- One-shot cleanup of API keys belonging to users who have already been
-- removed from their project. Pre-fix, RemoveProjectMember only deleted
-- the project_members row; the API keys stayed active. Flip those zombie
-- keys to status='revoked' so AuthMiddleware's existing
-- `apiKey.Status != "active"` check immediately starts rejecting them.
--
-- Idempotent: re-running this migration after the fix lands is a no-op,
-- because handleRemoveMember will keep this invariant going forward.

UPDATE api_keys
   SET status = 'revoked',
       updated_at = NOW()
 WHERE status = 'active'
   AND NOT EXISTS (
       SELECT 1 FROM project_members
        WHERE project_members.project_id = api_keys.project_id
          AND project_members.user_id    = api_keys.created_by
   );

-- Same orphan rule applied to oauth_grants: delete every grant whose
-- (project_id, user_id) has no project_members row. Pre-fix,
-- RemoveProjectMember never touched this table either. Hydra consent
-- sessions corresponding to these deleted rows are NOT revoked by the
-- migration — operators who want to revoke them must script that
-- directly against Hydra; the dashboard's per-removal flow handles new
-- removals automatically.
--
-- We use DELETE rather than a "status" flip because oauth_grants has
-- no status column. The introspection auth path catches the still-live
-- token via the Layer B membership check.

DELETE FROM oauth_grants
 WHERE NOT EXISTS (
     SELECT 1 FROM project_members
      WHERE project_members.project_id = oauth_grants.project_id
        AND project_members.user_id    = oauth_grants.user_id
 );
