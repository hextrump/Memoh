<template>
  <!-- "Connect apps" market: the existing per-bot Supermarket panel in a wide
       two-column dialog. Apps install into the current bot. -->
  <Dialog v-model:open="open">
    <DialogContent class="flex h-[min(44rem,85vh)] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
      <DialogHeader class="shrink-0 px-5 pb-1 pt-5">
        <DialogTitle>{{ t('messenger.market.title') }}</DialogTitle>
        <DialogDescription>
          {{ botLabel ? t('messenger.market.descriptionFor', { bot: botLabel }) : t('messenger.market.description') }}
        </DialogDescription>
      </DialogHeader>
      <PanelSupermarket
        :bot-id="botId"
        :can-manage="canManage"
        layout="grid"
        class="min-h-0 flex-1 px-3 pb-3"
      />
    </DialogContent>
  </Dialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@felinic/ui'
import PanelSupermarket from '@/components/sidebar/panel-supermarket.vue'

defineProps<{
  botId: string
  botLabel?: string
  canManage: boolean
}>()

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
</script>
