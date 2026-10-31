CREATE TEMP TABLE decoded AS
SELECT * FROM pg_logical_slot_get_changes(:'slot', NULL, NULL, 'skip-empty-xacts', '1');
TABLE decoded;
-- A division-by-zero error makes psql/ON_ERROR_STOP fail the test.
SELECT 1 / ((count(*) = :expected
  AND count(*) FILTER (WHERE data LIKE 'BEGIN %') = 1
  AND count(*) FILTER (WHERE data LIKE 'COMMIT %') = 1)::integer) AS assertion_passed
FROM decoded;
