// Keep official Radix rules for the controls used by Piko. This selects whole
// component families and never infers runtime states from template strings.
const families = /^(Base|Button|IconButton|TextField|TextArea|Select|Dialog|Tooltip|Switch|Skeleton|Text|Heading|Spinner|ScrollArea|Flex|reset$|variant-|r-)/;
export default {plugins: [{
  postcssPlugin: 'piko-selective-library-styles',
  Once(root) {
    const file = (root.source?.input?.file || '').replaceAll('\\', '/');
    if (file.endsWith('/@radix-ui/themes/components.css')) {
      root.walkRules(rule => {
        const names = [...rule.selector.matchAll(/\.rt-([A-Za-z][A-Za-z0-9-]*)/g)].map(match => match[1]);
        if (names.some(name => !families.test(name))) rule.remove();
      });
    }
    if (file.endsWith('/@radix-ui/themes/utilities.css')) {
      root.walkRules(rule => {
        if (rule.selector.includes('rt-r-') && !/rt-r-(max-w|w)(?![a-z])/.test(rule.selector)) rule.remove();
      });
    }
  },
}]};
