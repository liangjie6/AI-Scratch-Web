<script setup lang="ts">
import {computed} from 'vue';
import type {ImageGenerationResponse} from './image-generation-types';

const props = defineProps<{result: ImageGenerationResponse}>();
const imageUrl = computed(() => `data:${props.result.mime_type};base64,${props.result.image_base64}`);
</script>

<template>
  <figure class="sheet-selector">
    <figcaption class="sheet-selector__meta">
      <strong>生成结果</strong>
      <span>{{ props.result.sheet_width }} × {{ props.result.sheet_height }}</span>
    </figcaption>
    <img class="sheet-selector__image" :src="imageUrl" alt="AI 生成图片" />
  </figure>
</template>
