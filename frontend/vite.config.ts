import {defineConfig, type Plugin} from 'vite';
import vue from '@vitejs/plugin-vue';

// The standalone Scratch bundle was built with webpack's public path set to
// "/", so its lazy chunks are requested from /chunks/*. Keep the bundled
// assets under /assets/scratch and translate only that exact request prefix.
function scratchChunkRoute(): Plugin {
  const rewrite = (request: {url?: string}) => {
    if (request.url?.startsWith('/chunks/')) {
      request.url = '/assets/scratch' + request.url;
    }
  };
  return {
    name: 'scratch-chunk-route',
    configureServer(server) {
      server.middlewares.use((request, _response, next) => {
        rewrite(request);
        next();
      });
    },
    configurePreviewServer(server) {
      server.middlewares.use((request, _response, next) => {
        rewrite(request);
        next();
      });
    }
  };
}

export default defineConfig({
  plugins: [scratchChunkRoute(), vue()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': {
        target: process.env.AI_SCRATCH_API_ORIGIN ?? 'http://127.0.0.1:8000',
        changeOrigin: false
      }
    }
  },
  preview: {
    host: '0.0.0.0',
    port: 4173,
    proxy: {
      '/api': {
        target: process.env.AI_SCRATCH_API_ORIGIN ?? 'http://127.0.0.1:8000',
        changeOrigin: false
      }
    }
  },
  build: {
    target: 'es2022',
    sourcemap: false
  }
});
