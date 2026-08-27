<script setup lang="ts">
import {Image, LoaderCircle, UserRound, X} from 'lucide-vue-next';
import type {ScratchImportOption} from '../io/scratch-importer';

const props = defineProps<{options: ScratchImportOption[]; busy: boolean}>();
const emit = defineEmits<{close: []; select: [option: ScratchImportOption]}>();
</script>

<template>
  <div class="scratch-import-backdrop" @click.self="emit('close')">
    <section class="scratch-import-dialog" role="dialog" aria-modal="true" aria-labelledby="scratch-import-title">
      <header>
        <div><h2 id="scratch-import-title">从 Scratch 导入</h2><p>选择一个背景或角色</p></div>
        <button type="button" title="关闭" aria-label="关闭" :disabled="props.busy" @click="emit('close')"><X :size="17"/></button>
      </header>
      <div class="scratch-import-list">
        <button v-for="option in props.options" :key="option.id" type="button" :disabled="props.busy" @click="emit('select', option)">
          <span class="scratch-import-icon"><img v-if="option.previewUrl" :src="option.previewUrl" :alt="`${option.name}预览`"/><template v-else><Image v-if="option.kind === 'backdrop'" :size="19"/><UserRound v-else :size="19"/></template></span>
          <span><strong>{{ option.name }}</strong><small>{{ option.detail }}</small></span>
          <LoaderCircle v-if="props.busy" class="spin" :size="16"/>
        </button>
        <p v-if="!props.options.length" class="scratch-import-empty">当前 Scratch 项目中没有可导入的背景或角色</p>
      </div>
    </section>
  </div>
</template>
