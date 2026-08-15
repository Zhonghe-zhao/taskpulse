ALTER TABLE tasks
    ADD KEY idx_tasks_list_created (created_at, id),
    ADD KEY idx_tasks_list_status_created (status, created_at, id),
    ADD KEY idx_tasks_list_workflow_created (workflow, created_at, id);
