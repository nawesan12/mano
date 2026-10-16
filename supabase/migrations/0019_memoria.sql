-- Mano · Memoria con búsqueda: lo importante que entra se guarda con su vector y se busca por significado.
-- memory_chunks ya existe (0001, vector(1024)). Esta función la consulta.
create or replace function match_memory(query_embedding vector(1024), match_tenant uuid, match_count int default 6)
returns table (source text, ref text, content text, created_at timestamptz, similarity float)
language sql stable as $$
  select source, ref, content, created_at, 1 - (embedding <=> query_embedding) as similarity
  from memory_chunks
  where tenant_id = match_tenant and embedding is not null
  order by embedding <=> query_embedding
  limit least(match_count, 20)
$$;
create index if not exists memory_chunks_by_tenant on memory_chunks (tenant_id, created_at);
