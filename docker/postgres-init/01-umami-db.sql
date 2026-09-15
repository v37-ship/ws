-- Fresh-clone bootstrap for the Umami analytics database.
-- NOTE: runs only when pgdata is initialized from scratch. Existing volumes
-- need a one-time: docker exec <postgres-container> psql -U spa -d spa -c "CREATE DATABASE umami;"
SELECT 'CREATE DATABASE umami' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'umami')\gexec
