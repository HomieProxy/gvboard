<script setup lang="ts">
import { NButton, NSelect, NInput, NSpace, NPopover } from 'naive-ui'
import { ref } from 'vue'
import { conditions, type Filter } from '../helpers'

const props = defineProps<{ fields: { label: string; value: string }[] }>()
const model = defineModel<Filter[]>({ required: true })
const emit = defineEmits<{ apply: [] }>()
const show = ref(false)

function add() {
  model.value = [...model.value, { key: props.fields[0]?.value || '', condition: '=', value: '' }]
}
function remove(i: number) {
  model.value = model.value.filter((_, j) => j !== i)
}
function apply() {
  model.value = model.value.filter((f) => f.key && f.value !== '')
  show.value = false
  emit('apply')
}
function clear() {
  model.value = []
  show.value = false
  emit('apply')
}
</script>

<template>
  <NPopover v-model:show="show" trigger="click" placement="bottom-start" style="max-width: 640px">
    <template #trigger>
      <NButton :type="model.length ? 'primary' : 'default'" secondary>筛选{{ model.length ? `（${model.length}）` : '' }}</NButton>
    </template>
    <NSpace vertical>
      <div v-for="(f, i) in model" :key="i" class="row">
        <NSelect v-model:value="f.key" :options="fields" style="width: 170px" size="small" filterable />
        <NSelect v-model:value="f.condition" :options="conditions" style="width: 90px" size="small" />
        <NInput v-model:value="f.value" size="small" placeholder="值" style="width: 200px" @keyup.enter="apply" />
        <NButton size="small" quaternary type="error" @click="remove(i)">删除</NButton>
      </div>
      <div v-if="!model.length" class="muted">所有条件之间为“并且”关系</div>
      <NSpace>
        <NButton size="small" @click="add">添加条件</NButton>
        <NButton size="small" type="primary" @click="apply">应用</NButton>
        <NButton size="small" @click="clear">清空</NButton>
      </NSpace>
    </NSpace>
  </NPopover>
</template>

<style scoped>
.row { display: flex; gap: 6px; align-items: center; }
</style>
