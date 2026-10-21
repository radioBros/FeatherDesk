<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * QR code of an `otpauth://` URI (FE-071). Loaded lazily (defineAsyncComponent)
 * so the hand-written encoder in utils/qr.ts ships only in this chunk.
 */
import { computed } from 'vue'
import { encodeQr, qrSvgPath } from '@/utils/qr'

const props = withDefaults(defineProps<{ value: string; size?: number; label: string }>(), { size: 208 })
const qr = computed(() => encodeQr(props.value))
const box = computed(() => qr.value.size + 8)
</script>

<template>
  <svg
    :width="size"
    :height="size"
    :viewBox="`0 0 ${box} ${box}`"
    role="img"
    :aria-label="label"
    shape-rendering="crispEdges"
    data-testid="mfa-qr"
    style="background: #fff; border-radius: 4px"
  >
    <path :d="qrSvgPath(qr)" fill="#000" />
  </svg>
</template>
