ALTER TABLE tasks
    DROP INDEX idx_tasks_list_created,
    DROP INDEX idx_tasks_list_status_created,
    DROP INDEX idx_tasks_list_workflow_created;
