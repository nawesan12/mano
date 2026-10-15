import type { NextConfig } from 'next';
import { loadEnvConfig } from '@next/env';

// Usa el .env de la raíz del monorepo
loadEnvConfig(`${process.cwd()}/../..`);

const config: NextConfig = {};
export default config;
