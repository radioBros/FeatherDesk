/**
 * Local ESLint rules enforcing CONVENTIONS §12 (FE-005):
 *  - rules-from-validations: `:rules` on inputs must come from the central
 *    validations module (or a `*rules` value derived from it), never inline lambdas.
 *  - form-submit-validates: every <v-form> must submit through a useForm()
 *    `submit(...)` handler, which always calls validate() first.
 */
const INPUTS = new Set([
  'v-text-field', 'password-field', 'v-textarea', 'v-select', 'v-autocomplete', 'v-combobox', 'v-file-input', 'v-checkbox',
  'VTextField', 'PasswordField', 'VTextarea', 'VSelect', 'VAutocomplete', 'VCombobox', 'VFileInput', 'VCheckbox',
])
const RULE_ROOT = /^(validations|rules|\w*Rules)$/

function rootName(node) {
  let n = node
  while (n && (n.type === 'MemberExpression' || n.type === 'CallExpression' || n.type === 'ChainExpression')) {
    n = n.type === 'MemberExpression' ? n.object : n.type === 'ChainExpression' ? n.expression : n.callee
  }
  return n && n.type === 'Identifier' ? n.name : null
}

function isAllowedRulesExpr(node) {
  if (!node) return false
  switch (node.type) {
    case 'Identifier':
    case 'MemberExpression':
    case 'CallExpression':
    case 'ChainExpression':
      return RULE_ROOT.test(rootName(node) ?? '')
    case 'ArrayExpression':
      return node.elements.every((e) => e && (e.type === 'SpreadElement' ? isAllowedRulesExpr(e.argument) : isAllowedRulesExpr(e)))
    case 'ConditionalExpression':
      return isAllowedRulesExpr(node.consequent) && isAllowedRulesExpr(node.alternate)
    case 'LogicalExpression':
      return isAllowedRulesExpr(node.right) && (node.left.type !== 'ArrowFunctionExpression')
    default:
      return false
  }
}

function containsInlineFunction(node) {
  if (!node || typeof node !== 'object') return false
  if (node.type === 'ArrowFunctionExpression' || node.type === 'FunctionExpression') return true
  if (node.type === 'ArrayExpression') return node.elements.some((e) => containsInlineFunction(e))
  return false
}

function bindAttr(element, name) {
  return element.startTag.attributes.find(
    (a) => a.directive && a.key.name && a.key.name.name === 'bind' && a.key.argument && a.key.argument.name === name,
  )
}

function onAttr(element, name) {
  return element.startTag.attributes.find(
    (a) => a.directive && a.key.name && a.key.name.name === 'on' && a.key.argument && a.key.argument.name === name,
  )
}

const rulesFromValidations = {
  meta: {
    type: 'problem',
    docs: { description: 'Input :rules must come from src/constants/validations.ts (CONVENTIONS §12)' },
    messages: {
      inline: ':rules must reference validations.* (or a *rules value derived from it), not inline functions.',
      inlineScript: 'Define rules with validations.* instead of inline functions (CONVENTIONS §12).',
    },
    schema: [],
  },
  create(context) {
    const filename = context.filename ?? context.getFilename()
    if (/constants[\\/]validations\.ts$/.test(filename)) return {}
    const ps = context.sourceCode.parserServices
    const scriptVisitor = {
      VariableDeclarator(node) {
        if (node.id.type !== 'Identifier' || !/rules$/i.test(node.id.name) || !node.init) return
        let init = node.init
        // computed(() => [...])
        if (init.type === 'CallExpression' && init.arguments[0] && init.arguments[0].type === 'ArrowFunctionExpression') {
          init = init.arguments[0].body
        }
        if (init.type === 'ArrayExpression' && containsInlineFunction(init)) context.report({ node, messageId: 'inlineScript' })
      },
    }
    if (!ps || !ps.defineTemplateBodyVisitor) return scriptVisitor
    return ps.defineTemplateBodyVisitor(
      {
        VElement(el) {
          if (!INPUTS.has(el.rawName)) return
          const attr = bindAttr(el, 'rules')
          if (!attr || !attr.value || !attr.value.expression) return
          if (!isAllowedRulesExpr(attr.value.expression)) context.report({ node: attr, messageId: 'inline' })
        },
      },
      scriptVisitor,
    )
  },
}

const formSubmitValidates = {
  meta: {
    type: 'problem',
    docs: { description: '<v-form> must submit through useForm().submit(...) so validate() runs first' },
    messages: {
      missing: '<v-form> needs @submit.prevent bound to a handler created with useForm().submit(...).',
      bypass: 'The <v-form> submit handler "{{name}}" must be created with useForm().submit(...) (or call validate() first).',
    },
    schema: [],
  },
  create(context) {
    const ps = context.sourceCode.parserServices
    if (!ps || !ps.defineTemplateBodyVisitor) return {}
    const handlers = new Map() // name -> ok
    const src = context.sourceCode
    return ps.defineTemplateBodyVisitor(
      {
        VElement(el) {
          if (el.rawName !== 'v-form' && el.rawName !== 'VForm') return
          const attr = onAttr(el, 'submit')
          if (!attr || !attr.value || !attr.value.expression) {
            context.report({ node: el.startTag, messageId: 'missing' })
            return
          }
          const expr = attr.value.expression
          if (expr.type === 'Identifier') {
            if (handlers.get(expr.name) !== true) context.report({ node: attr, messageId: 'bypass', data: { name: expr.name } })
          } else if (expr.type === 'CallExpression') {
            const callee = expr.callee
            const name = callee.type === 'Identifier' ? callee.name : callee.type === 'MemberExpression' ? callee.property.name : ''
            if (name !== 'submit' && handlers.get(name) !== true)
              context.report({ node: attr, messageId: 'bypass', data: { name: name || '(expression)' } })
          } else {
            context.report({ node: attr, messageId: 'bypass', data: { name: '(expression)' } })
          }
        },
      },
      {
        VariableDeclarator(node) {
          if (node.id.type !== 'Identifier' || !node.init) return
          const init = node.init
          const callee = init.type === 'CallExpression' ? init.callee : null
          const viaSubmit =
            callee &&
            ((callee.type === 'Identifier' && callee.name === 'submit') ||
              (callee.type === 'MemberExpression' && callee.property.name === 'submit'))
          const fn = init.type === 'ArrowFunctionExpression' || init.type === 'FunctionExpression'
          handlers.set(node.id.name, !!viaSubmit || (fn && /\.validate\(\)/.test(src.getText(init))))
        },
        FunctionDeclaration(node) {
          if (node.id) handlers.set(node.id.name, /\.validate\(\)/.test(src.getText(node)))
        },
      },
    )
  },
}

export default {
  rules: {
    'rules-from-validations': rulesFromValidations,
    'form-submit-validates': formSubmitValidates,
  },
}
