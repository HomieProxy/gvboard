<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import {
  AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@gv/shared/ui/alert-dialog'
import { Button } from '@gv/shared/ui/button'
import { confirmState } from '../ui'

const { t } = useI18n()

function cancel() {
  if (confirmState.busy) return
  confirmState.open = false
  confirmState.resolve(false)
}
</script>

<template>
  <AlertDialog :open="confirmState.open" @update:open="(v: boolean) => !v && cancel()">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>{{ t('common.confirm') }}</AlertDialogTitle>
        <AlertDialogDescription class="whitespace-pre-line">{{ confirmState.content }}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <Button variant="outline" :disabled="confirmState.busy" @click="cancel">{{ t('common.cancel') }}</Button>
        <Button :variant="confirmState.danger ? 'destructive' : 'default'" :disabled="confirmState.busy" @click="confirmState.action()">
          {{ t('common.ok') }}
        </Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
