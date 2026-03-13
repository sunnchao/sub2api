<template>
  <div v-if="groups && groups.length > 0" class="flex flex-col gap-1 py-1">
    <GroupBadge
      v-for="group in groups"
      :key="group.id"
      :name="group.name"
      :platform="group.platform"
      :subscription-type="group.subscription_type"
      :rate-multiplier="group.rate_multiplier"
      :show-rate="false"
      class="w-fit"
    />
  </div>
  <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
</template>

<script setup lang="ts">
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { Group } from '@/types'

interface Props {
  groups: Group[] | null | undefined
}

defineProps<Props>()
</script>
