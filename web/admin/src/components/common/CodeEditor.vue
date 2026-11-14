<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { basicSetup } from 'codemirror'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, keymap, placeholder as placeholderExt } from '@codemirror/view'
import { indentWithTab } from '@codemirror/commands'
import { StreamLanguage, indentUnit, syntaxHighlighting } from '@codemirror/language'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { properties } from '@codemirror/legacy-modes/mode/properties'
import { html } from '@codemirror/legacy-modes/mode/xml'
import { classHighlighter } from '@lezer/highlight'

const props = withDefaults(defineProps<{ modelValue: string; lang?: string; placeholder?: string }>(), { lang: 'text', placeholder: '' })
const emit = defineEmits<{ 'update:modelValue': [v: string] }>()

const el = ref<HTMLDivElement>()
let view: EditorView | undefined

function language(lang: string): Extension {
  if (lang === 'yaml') return [yaml(), indentUnit.of('  ')]
  if (lang === 'json') return json()
  if (lang === 'html') return [StreamLanguage.define(html), indentUnit.of('  ')]
  // Surge / QuantumultX 等 INI 风格的模板
  return StreamLanguage.define(properties)
}

const theme = EditorView.theme({
  '&': { fontSize: '12px', border: '1px solid var(--border)', borderRadius: 'calc(var(--radius) - 2px)', backgroundColor: 'var(--background)' },
  '&.cm-focused': { outline: 'none', borderColor: 'var(--ring)', boxShadow: '0 0 0 3px color-mix(in oklab, var(--ring) 50%, transparent)' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', minHeight: '16rem', maxHeight: '36rem' },
  '.cm-gutters': { backgroundColor: 'var(--muted)', color: 'var(--muted-foreground)', borderRight: '1px solid var(--border)' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'color-mix(in oklab, var(--muted) 70%, transparent)' },
  '.cm-content': { caretColor: 'var(--foreground)' },
  '.cm-placeholder': { color: 'var(--muted-foreground)' },
})

onMounted(() => {
  view = new EditorView({
    parent: el.value!,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        basicSetup,
        keymap.of([indentWithTab]),
        language(props.lang),
        syntaxHighlighting(classHighlighter),
        placeholderExt(props.placeholder),
        theme,
        EditorView.updateListener.of((u) => {
          if (u.docChanged) emit('update:modelValue', u.state.doc.toString())
        }),
      ],
    }),
  })
})

watch(
  () => props.modelValue,
  (v) => {
    if (view && v !== view.state.doc.toString()) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } })
  },
)

onBeforeUnmount(() => view?.destroy())
</script>

<template>
  <div ref="el" class="code-editor" />
</template>

<style>
.code-editor .tok-comment { color: #6a737d; font-style: italic; }
.code-editor .tok-propertyName, .code-editor .tok-definition { color: #005cc5; }
.code-editor .tok-string, .code-editor .tok-string2 { color: #22863a; }
.code-editor .tok-number { color: #b45309; }
.code-editor .tok-bool, .code-editor .tok-atom, .code-editor .tok-keyword { color: #d73a49; }
.code-editor .tok-meta, .code-editor .tok-typeName, .code-editor .tok-labelName { color: #6f42c1; }
.code-editor .tok-punctuation, .code-editor .tok-operator { color: #57606a; }
.dark .code-editor .tok-comment { color: #8b949e; }
.dark .code-editor .tok-propertyName, .dark .code-editor .tok-definition { color: #79c0ff; }
.dark .code-editor .tok-string, .dark .code-editor .tok-string2 { color: #a5d6ff; }
.dark .code-editor .tok-number { color: #f0b072; }
.dark .code-editor .tok-bool, .dark .code-editor .tok-atom, .dark .code-editor .tok-keyword { color: #ff7b72; }
.dark .code-editor .tok-meta, .dark .code-editor .tok-typeName, .dark .code-editor .tok-labelName { color: #d2a8ff; }
.dark .code-editor .tok-punctuation, .dark .code-editor .tok-operator { color: #8b949e; }
</style>
