-- Demo data recreating a slice of the original Timesheet.xlsx so results can be checked
-- against the spreadsheet. Only runs on an empty app schema (start cmd/app once first).
--
--   docker compose exec -T postgres psql -U crm -d crm -v ON_ERROR_STOP=1 < scripts/demo_seed.sql
--
-- What to expect afterwards:
--   * Invoices > New invoice, client Demo Builder Co., hours 4/27–5/3/2026:
--       Hyrum 23 — Justin 47 h x $35 = $1,645                                       (invoice 4)
--   * Same client, hours 5/4–5/10: Smithfield 15 — 42 + 42 + 44 = 128 h x $20 = $2,560 (invoice 5)
--   * Project profit (by bid): Hyrum Lot 117 — 97.75 h, bid $4,170, labor $1,955, profit $2,215,
--     20% share $443, Jake's pay $1,088 (Bid Jobs sheet, row 24)

BEGIN;
SET LOCAL search_path TO app;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM workers) OR EXISTS (SELECT 1 FROM projects) THEN
    RAISE EXCEPTION 'app schema already has data; the demo seed only runs on an empty database';
  END IF;
END $$;

UPDATE company_settings SET name = 'Demo Roofing & Framing', address = '12 Main St' || chr(10) || 'Hyrum, UT 84319',
  phone = '435-555-0100', email = 'office@example.com', invoice_footer = 'Checks payable to Demo Roofing & Framing. Thank you!'
WHERE id = 1;

INSERT INTO clients (name, contact_name, email, notes)
VALUES ('Demo Builder Co.', 'Pat Builder', 'pat@example.com', 'Sample client created by scripts/demo_seed.sql');

INSERT INTO workers (name, pay_rate_cents, notes) VALUES
  ('Justin M', 0, 'Owner — logs hours for billing and job costing, not paid wages'),
  ('Abbey M', 0, 'Owner — logs hours for billing and job costing, not paid wages'),
  ('Jake H', 2000, 'Employee — $20/h plus profit share on bid projects'),
  ('Clayton', 2500, '');

INSERT INTO task_templates (name, description) VALUES ('Roofing lot', 'Standard phases for a bid roofing lot');
INSERT INTO task_template_items (template_id, title, position)
SELECT t.id, v.title, v.pos FROM task_templates t, (VALUES ('Dry in', 0), ('Shingling', 1), ('Cleanup', 2)) AS v(title, pos);

INSERT INTO projects (client_id, name, billing_type, bill_rate_cents, bid_amount_cents, labor_cost_rate_cents,
                      profit_share_bps, profit_share_worker_id, status, completed_on)
SELECT c.id, v.name, v.billing, v.rate, v.bid, 2000, v.share,
       CASE WHEN v.share > 0 THEN (SELECT id FROM workers WHERE name = 'Jake H') END,
       v.status, v.completed::date
FROM clients c, (VALUES
  ('Hyrum 23',      'hourly', 3500, 0,      0,    'active', NULL::text),
  ('Hyrum Lot 117', 'fixed',  0,    417000, 2000, 'done',   '2026-06-17'),
  ('Smithfield 15', 'hourly', 2000, 0,      0,    'active', NULL)
) AS v(name, billing, rate, bid, share, status, completed);

-- Lot 117 tasks come from the template; hourly projects get a general task.
INSERT INTO tasks (project_id, title, status, position)
SELECT p.id, i.title, 'done', i.position FROM projects p, task_template_items i
WHERE p.name = 'Hyrum Lot 117' AND i.title IN ('Dry in', 'Shingling');
INSERT INTO tasks (project_id, title, position)
SELECT p.id, 'Framing', 0 FROM projects p WHERE p.billing_type = 'hourly';

-- Times are minutes after midnight (420 = 7:00 AM). Minutes = end - start - break.
INSERT INTO time_entries (worker_id, project_id, task_id, work_date, start_minute, end_minute, break_minutes, minutes, pay_rate_cents, notes)
SELECT w.id, p.id, t.id, v.d::date, v.s, v.e, v.b,
       coalesce(v.e - v.s - v.b, v.m), w.pay_rate_cents, v.notes
FROM (VALUES
  -- Invoice 4: Hyrum 23, week of 4/27 (Justin)
  ('Justin M', 'Hyrum 23', 'Framing'::text, '2026-04-27', 600, 1230, 30, NULL::int, ''),
  ('Justin M', 'Hyrum 23', 'Framing', '2026-04-28', 450, 960, 30, NULL, ''),
  ('Justin M', 'Hyrum 23', 'Framing', '2026-04-29', 450, 960, 30, NULL, ''),
  ('Justin M', 'Hyrum 23', 'Framing', '2026-04-30', 420, 1080, 90, NULL, ''),
  ('Justin M', 'Hyrum 23', 'Framing', '2026-05-01', 450, 1170, 30, NULL, ''),
  -- Invoice 5: Smithfield 15, week of 5/4 (everyone)
  ('Justin M', 'Smithfield 15', 'Framing', '2026-05-04', 420, 1110, 30, NULL, ''),
  ('Justin M', 'Smithfield 15', 'Framing', '2026-05-06', 420, 990, 30, NULL, ''),
  ('Justin M', 'Smithfield 15', 'Framing', '2026-05-07', 420, 990, 30, NULL, ''),
  ('Justin M', 'Smithfield 15', 'Framing', '2026-05-08', 420, 1230, 30, NULL, ''),
  ('Abbey M', 'Smithfield 15', 'Framing', '2026-05-04', 420, 1110, 30, NULL, ''),
  ('Abbey M', 'Smithfield 15', 'Framing', '2026-05-06', 420, 990, 30, NULL, ''),
  ('Abbey M', 'Smithfield 15', 'Framing', '2026-05-07', 420, 990, 30, NULL, ''),
  ('Abbey M', 'Smithfield 15', 'Framing', '2026-05-08', 420, 1230, 30, NULL, ''),
  ('Jake H', 'Smithfield 15', 'Framing', '2026-05-04', 420, 1110, 30, NULL, ''),
  ('Jake H', 'Smithfield 15', 'Framing', '2026-05-06', 420, 990, 30, NULL, ''),
  ('Jake H', 'Smithfield 15', 'Framing', '2026-05-07', 420, 990, 30, NULL, ''),
  ('Jake H', 'Smithfield 15', 'Framing', '2026-05-08', 420, 1230, 30, NULL, ''),
  ('Jake H', 'Smithfield 15', NULL, '2026-05-08', NULL, NULL, 0, 120, 'Extra (hours-only entry)'),
  -- Lot 117: dry in (Sat 5/9) and shingling (6/15–6/17)
  ('Justin M', 'Hyrum Lot 117', 'Dry in', '2026-05-09', 420, 1020, 30, NULL, ''),
  ('Abbey M', 'Hyrum Lot 117', 'Dry in', '2026-05-09', 420, 1020, 30, NULL, ''),
  ('Jake H', 'Hyrum Lot 117', 'Dry in', '2026-05-09', 420, 1020, 30, NULL, ''),
  ('Justin M', 'Hyrum Lot 117', 'Shingling', '2026-06-15', 450, 1080, 30, NULL, ''),
  ('Justin M', 'Hyrum Lot 117', 'Shingling', '2026-06-16', 375, 945, 30, NULL, ''),
  ('Justin M', 'Hyrum Lot 117', 'Shingling', '2026-06-17', 420, 960, 30, NULL, ''),
  ('Abbey M', 'Hyrum Lot 117', 'Shingling', '2026-06-15', 450, 1080, 30, NULL, ''),
  ('Abbey M', 'Hyrum Lot 117', 'Shingling', '2026-06-16', 375, 945, 30, NULL, ''),
  ('Jake H', 'Hyrum Lot 117', 'Shingling', '2026-06-15', 510, 1080, 30, NULL, ''),
  ('Jake H', 'Hyrum Lot 117', 'Shingling', '2026-06-16', 510, 945, 30, NULL, ''),
  ('Jake H', 'Hyrum Lot 117', 'Shingling', '2026-06-17', 510, 960, 30, NULL, '')
) AS v(worker, project, task, d, s, e, b, m, notes)
JOIN workers w ON w.name = v.worker
JOIN projects p ON p.name = v.project
LEFT JOIN tasks t ON t.project_id = p.id AND t.title = v.task;

COMMIT;
