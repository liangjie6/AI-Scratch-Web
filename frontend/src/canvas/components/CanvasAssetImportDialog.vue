<script setup lang="ts">
import {nextTick, ref, watch} from 'vue';
import {Images, LoaderCircle, X} from 'lucide-vue-next';
import {listGeneratedAssets, type GeneratedAsset} from '../../ai/asset-library-store';
const props=defineProps<{open:boolean;busy:boolean;importError?:string}>();
const emit=defineEmits<{close:[];choose:[asset:GeneratedAsset]}>();
const assets=ref<GeneratedAsset[]>([]),loading=ref(false),error=ref('');
const closeButton=ref<HTMLButtonElement|null>(null);
let returnFocus:HTMLElement|null=null;
async function load(){loading.value=true;error.value='';try{assets.value=await listGeneratedAssets();}catch(e){error.value=e instanceof Error?e.message:'素材读取失败';}finally{loading.value=false;}}
watch(()=>props.open,async open=>{if(open){returnFocus=document.activeElement as HTMLElement|null;void load();await nextTick();closeButton.value?.focus();}else{await nextTick();returnFocus?.focus();}});
function onKey(event:KeyboardEvent){if(event.key==='Escape'){event.stopPropagation();if(!props.busy)emit('close');}if(event.key==='Tab'){const dialog=event.currentTarget as HTMLElement,items=Array.from(dialog.querySelectorAll<HTMLElement>('button:not(:disabled)'));const first=items[0],last=items.at(-1);if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}}}
</script>
<template>
  <Teleport to="body">
    <div v-if="open" class="asset-library-overlay" @click.self="!busy&&emit('close')" @keydown="onKey">
      <section class="asset-library" role="dialog" aria-modal="true" aria-labelledby="canvas-library-title">
        <header class="asset-library__header"><div><h2 id="canvas-library-title"><Images :size="20"/>从素材库导入</h2><p>选择素材，添加为当前画布的新图层</p></div><button ref="closeButton" class="icon-button" type="button" aria-label="关闭素材库" :disabled="busy" @click="emit('close')"><X :size="18"/></button></header>
        <div class="asset-library__content"><p v-if="importError" role="alert">{{importError}}</p>
          <div v-if="loading" class="asset-library__loading" role="status"><LoaderCircle class="spin"/>正在读取本地素材</div>
          <div v-else-if="error" class="asset-library__error" role="alert"><strong>{{error}}</strong><button class="button" type="button" @click="load">重试</button></div>
          <div v-else-if="!assets.length" class="asset-library__empty"><Images :size="30"/><strong>还没有本地素材</strong><span>生成的素材会自动保存在这里，也可以直接导入本地图片。</span></div>
          <div v-else class="asset-library__grid"><article v-for="asset in assets" :key="asset.id" class="asset-card"><div class="asset-card__preview"><img :src="'data:'+asset.result.mime_type+';base64,'+asset.result.image_base64" :alt="asset.name"/></div><div class="asset-card__body"><strong class="asset-card__name">{{asset.name}}</strong><p>{{asset.prompt}}</p><button class="button button--primary" type="button" :disabled="busy" :aria-label="'导入素材 '+asset.name" @click="emit('choose',asset)"><LoaderCircle v-if="busy" class="spin" :size="14"/>导入到画布</button></div></article></div>
        </div>
      </section>
    </div>
  </Teleport>
</template>
