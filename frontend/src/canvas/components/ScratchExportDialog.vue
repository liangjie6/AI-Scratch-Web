<script setup lang="ts">
import {Image, LoaderCircle, UserRound, X} from 'lucide-vue-next';

defineProps<{busy: boolean}>();
const emit = defineEmits<{close: []; select: [target: 'sprite' | 'backdrop']}>();
</script>

<template>
  <div class="scratch-import-backdrop" @click.self="emit('close')">
    <section class="scratch-import-dialog" role="dialog" aria-modal="true" aria-labelledby="scratch-export-title">
      <header>
        <div><h2 id="scratch-export-title">导入 Scratch</h2><p>选择画布内容在 Scratch 中的用途</p></div>
        <button type="button" title="关闭" aria-label="关闭" :disabled="busy" @click="emit('close')"><X :size="17" /></button>
      </header>
      <div class="scratch-import-list scratch-target-list">
        <button type="button" :disabled="busy" @click="emit('select', 'sprite')">
          <span class="scratch-import-icon"><UserRound :size="22" /></span>
          <span><strong>导入为角色</strong><small>造型会自动缩放到舞台范围内</small></span>
          <LoaderCircle v-if="busy" class="spin" :size="16" />
        </button>
        <button type="button" :disabled="busy" @click="emit('select', 'backdrop')">
          <span class="scratch-import-icon"><Image :size="22" /></span>
          <span><strong>导入为背景</strong><small>完整画板会适配为 480 × 360 舞台背景</small></span>
          <LoaderCircle v-if="busy" class="spin" :size="16" />
        </button>
      </div>
    </section>
  </div>
</template>
