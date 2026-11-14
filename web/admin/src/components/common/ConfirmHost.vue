<script setup lang="ts">
import {
  AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@gv/shared/ui/alert-dialog'
import { Button } from '@gv/shared/ui/button'
import { confirmState } from '@/ui'

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
        <AlertDialogTitle>{{ confirmState.title }}</AlertDialogTitle>
        <AlertDialogDescription class="whitespace-pre-line">{{ confirmState.content }}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <Button variant="outline" :disabled="confirmState.busy" @click="cancel">取消</Button>
        <Button :variant="confirmState.danger ? 'destructive' : 'default'" :disabled="confirmState.busy" @click="confirmState.action()">
          确定
        </Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
