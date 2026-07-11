-- 063_project_member_role_check.sql
--
-- project_members.role was historically unconstrained TEXT. Unknown values
-- are dangerous because several authorization paths distinguish only the
-- developer role and otherwise grant broader project visibility. Normalize
-- any pre-existing unknown value to the least-privileged valid role before
-- installing the database invariant.

UPDATE project_members
   SET role = 'developer'
 WHERE role NOT IN ('owner', 'maintainer', 'developer');

ALTER TABLE project_members
    DROP CONSTRAINT IF EXISTS project_members_role_check;

ALTER TABLE project_members
    ADD CONSTRAINT project_members_role_check
    CHECK (role IN ('owner', 'maintainer', 'developer'));
