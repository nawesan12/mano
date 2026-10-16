#!/usr/bin/env bash
# Aplica las migraciones en orden y registra cuáles ya corrieron (no repite ninguna).
#   DATABASE_URL="postgresql://postgres:…@db.xxxx.supabase.co:5432/postgres" ./supabase/aplicar.sh
# El DATABASE_URL sale de Supabase → Project Settings → Database → Connection string (URI).
set -euo pipefail
: "${DATABASE_URL:?Falta DATABASE_URL}"
cd "$(dirname "$0")"
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "create table if not exists mano_migrations (name text primary key, applied_at timestamptz not null default now())"
for f in migrations/*.sql; do
  name=$(basename "$f")
  if [ "$(psql "$DATABASE_URL" -tA -c "select 1 from mano_migrations where name = '$name'")" = "1" ]; then
    continue
  fi
  echo "→ $name"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q --single-transaction -f "$f" -c "insert into mano_migrations (name) values ('$name')"
done
if [ "${SEED:-}" = "1" ]; then
  echo "→ seed.sql"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -f seed.sql
fi
echo "Listo."
