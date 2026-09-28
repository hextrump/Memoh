<template>
  <!-- "To: search or create a bot" — the new-conversation entry point. Picking a
       bot opens a fresh chat with it; the create group routes to the built-in
       Bot Designer (conversational) or the quick-create dialog. -->
  <CommandDialog
    v-model:open="open"
    :title="t('messenger.picker.title')"
    :description="t('messenger.picker.description')"
  >
    <!-- The "To:" label shares the search row; CommandInput's own wrapper is
         stretched and its divider hoisted onto this row. -->
    <div class="flex items-center gap-2 border-b border-border-soft px-3.5 text-control [&>[data-slot=command-input-wrapper]]:flex-1 [&>[data-slot=command-input-wrapper]]:border-0 [&>[data-slot=command-input-wrapper]]:px-0">
      <span class="shrink-0 text-muted-foreground">{{ t('messenger.picker.to') }}</span>
      <CommandInput
        :search-icon="false"
        size="md"
        :placeholder="t('messenger.picker.placeholder')"
      />
    </div>
    <CommandList class="max-h-96">
      <CommandEmpty>{{ t('messenger.noMatches') }}</CommandEmpty>
      <CommandGroup :heading="t('messenger.picker.createGroup')">
        <CommandItem
          v-if="designerBot"
          :value="`create-designer ${t('messenger.picker.chatWithDesigner')}`"
          class="gap-3"
          @select="startWith(designerBot)"
        >
          <span class="flex size-8 shrink-0 items-center justify-center rounded-full bg-accent text-foreground">
            <Sparkles class="size-4" />
          </span>
          <span class="flex min-w-0 flex-col">
            <span class="truncate font-medium">{{ t('messenger.picker.chatWithDesigner') }}</span>
            <span class="truncate text-xs text-muted-foreground">{{ t('messenger.picker.chatWithDesignerHint') }}</span>
          </span>
        </CommandItem>
        <CommandItem
          :value="`create-quick ${t('messenger.picker.quickCreate')}`"
          class="gap-3"
          @select="emitQuickCreate"
        >
          <span class="flex size-8 shrink-0 items-center justify-center rounded-full bg-accent text-foreground">
            <Plus class="size-4" />
          </span>
          <span class="flex min-w-0 flex-col">
            <span class="truncate font-medium">{{ t('messenger.picker.quickCreate') }}</span>
            <span class="truncate text-xs text-muted-foreground">{{ t('messenger.picker.quickCreateHint') }}</span>
          </span>
        </CommandItem>
      </CommandGroup>
      <CommandSeparator v-if="selectableBots.length" />
      <CommandGroup
        v-if="selectableBots.length"
        :heading="t('messenger.picker.botsGroup')"
      >
        <CommandItem
          v-for="bot in selectableBots"
          :key="bot.id"
          :value="`${bot.display_name ?? ''} ${bot.name ?? ''} ${bot.id}`"
          class="gap-3"
          @select="startWith(bot)"
        >
          <Avatar class="size-8 shrink-0">
            <AvatarImage
              v-if="bot.avatar_url"
              :src="bot.avatar_url"
              :alt="labelOf(bot)"
            />
            <AvatarFallback class="text-xs">
              {{ botInitials(labelOf(bot)) }}
            </AvatarFallback>
          </Avatar>
          <span class="min-w-0 flex-1 truncate">{{ labelOf(bot) }}</span>
          <span class="shrink-0 text-xs text-muted-foreground">{{ t('messenger.picker.newChat') }}</span>
        </CommandItem>
      </CommandGroup>
    </CommandList>
  </CommandDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useQuery } from '@pinia/colada'
import { getBotsQuery } from '@memohai/sdk/colada'
import type { BotsBot } from '@memohai/sdk'
import {
  Avatar,
  AvatarFallback,
  AvatarImage,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@felinic/ui'
import { Plus, Sparkles } from 'lucide-vue-next'
import { useChatStore } from '@/store/chat-list'
import { useWorkspaceTabsStore } from '@/store/workspace-tabs'
import { botInitials } from '@/utils/bot-initials'

const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ 'quick-create': [] }>()

const { t } = useI18n()
const router = useRouter()
const chatStore = useChatStore()
const workspaceTabs = useWorkspaceTabsStore()

const { data: botData } = useQuery(getBotsQuery())

// Matches BotDesignerMetadataKey on the server.
const isDesigner = (bot: BotsBot) => bot.metadata?.bot_designer === true

const selectableBots = computed(() =>
  (botData.value?.items ?? []).filter(bot =>
    bot.id && bot.status !== 'error' && bot.status !== 'creating' && bot.status !== 'deleting',
  ),
)
const designerBot = computed(() => selectableBots.value.find(isDesigner) ?? null)

function labelOf(bot: BotsBot): string {
  return bot.display_name || bot.name || bot.id || ''
}

async function startWith(bot: BotsBot | null) {
  const id = bot?.id ?? ''
  if (!id) return
  open.value = false
  workspaceTabs.closeMobileNav()
  if (id !== chatStore.currentBotId) {
    await chatStore.selectBot(id)
    await router.push({ name: 'bot', params: { botName: bot?.name ?? id } })
  }
  workspaceTabs.openDraftChat({ title: t('chat.newSession'), explicitSelection: false })
}

function emitQuickCreate() {
  open.value = false
  emit('quick-create')
}
</script>
