-- Fresh-clone bootstrap for the Evolution API (WhatsApp) database.
-- NOTE: runs only when pgdata is initialized from scratch. Existing volumes
-- need a one-time: docker exec <postgres-container> psql -U spa -d spa -c "CREATE DATABASE evolution;"
SELECT 'CREATE DATABASE evolution' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'evolution')\gexec
