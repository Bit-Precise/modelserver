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
