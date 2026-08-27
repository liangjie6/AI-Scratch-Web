import {createApp} from 'vue';
import App from './app/App.vue';
import './styles/local-ui.css';
import './styles/ai-blocks.css';
import './styles/canvas-workspace.css';

if (import.meta.hot) {
  import.meta.hot.on('vite:beforeUpdate', payload => {
    const hostChanged = payload.updates.some(update =>
      update.path.endsWith('/src/app/App.vue') ||
      update.path.endsWith('/src/scratch/ScratchHost.vue')
    );
    if (hostChanged) window.location.reload();
  });
}

createApp(App).mount('#app');

