<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script lang="ts">
/**
 * Wraps the cells of one table row and renders them in the user's column order
 * (useTableColumns). A cell takes part when it carries `data-col="<key>"`; the
 * others (selection, actions) keep their place before or after them. With
 * `head`, header cells also get their stored width and a ColumnResizer, and the
 * table switches to fixed widths once the user has resized a column.
 *
 *   <tr><ColumnCells :cols="cols" head>
 *     <th v-if="cols.show('name')" data-col="name">Name</th> …
 *   </ColumnCells></tr>
 */
import { Comment, Fragment, cloneVNode, createTextVNode, defineComponent, getCurrentInstance, h, mergeProps, onMounted, onUpdated, type PropType, type VNode } from 'vue'
import type { TableColumns } from '@/composables/useTableColumns'
import { DEFAULT_COLUMN_WIDTH, syncTableLayout } from '@/composables/columnLayout'
import ColumnResizer from './ColumnResizer.vue'

function flatten(nodes: VNode[]): VNode[] {
  return nodes.flatMap((n) => (n.type === Fragment && Array.isArray(n.children) ? flatten(n.children as VNode[]) : n.type === Comment ? [] : [n]))
}

const colOf = (n: VNode): string | undefined => (n.props?.['data-col'] as string | undefined) ?? undefined

function childrenOf(n: VNode): VNode[] {
  if (typeof n.children === 'string') return [createTextVNode(n.children)]
  return Array.isArray(n.children) ? (n.children as VNode[]) : []
}

export default defineComponent({
  name: 'ColumnCells',
  props: {
    cols: { type: Object as PropType<TableColumns>, required: true },
    head: Boolean,
  },
  setup(props, { slots }) {
    const inst = getCurrentInstance()!
    let total = 0
    const table = () => ((inst.subTree?.el as Node | null)?.parentElement?.closest('table') as HTMLTableElement | null) ?? null
    const sync = () => {
      if (props.head) syncTableLayout(table(), total)
    }
    onMounted(sync)
    onUpdated(sync)

    return () => {
      const nodes = flatten(slots.default?.() ?? [])
      const first = nodes.findIndex((n) => colOf(n))
      if (first < 0) return nodes
      let last = first
      nodes.forEach((n, i) => {
        if (colOf(n)) last = i
      })
      const rank = props.cols.rank.value
      const middle = nodes.slice(first, last + 1)
      const keyed = middle.filter((n) => colOf(n)).sort((a, b) => (rank.get(colOf(a)!) ?? 0) - (rank.get(colOf(b)!) ?? 0))
      const ordered = [...nodes.slice(0, first), ...keyed, ...middle.filter((n) => !colOf(n)), ...nodes.slice(last + 1)]

      if (!props.head) return ordered.map((n, i) => cloneVNode(n, { key: colOf(n) ? `c:${colOf(n)}` : `u:${i}` }))

      const sized = props.cols.sized.value
      let unkeyed = 0
      total = 0
      return ordered.map((n, i) => {
        const col = colOf(n)
        const w = sized ? (props.cols.width(col ?? `:${unkeyed}`) ?? DEFAULT_COLUMN_WIDTH) : undefined
        if (!col) unkeyed++
        if (w) total += w
        const style = w ? { width: `${w}px`, minWidth: `${w}px`, maxWidth: `${w}px` } : undefined
        if (!col) return cloneVNode(n, { key: `u:${i}`, ...(style ? { style } : {}) })
        const label = props.cols.all.value.find((c) => c.key === col)?.label
        return h(n.type as string, mergeProps(n.props ?? {}, { key: `c:${col}`, ...(style ? { style } : {}) }), [
          ...childrenOf(n),
          h(ColumnResizer, { cols: props.cols, col, label }),
        ])
      })
    }
  },
})
</script>
