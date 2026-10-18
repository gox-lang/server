import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

test('Action forms preserve typed inputs including unchecked checkboxes', async () => {
 const source = await readFile(new URL('./runtime.go', import.meta.url), 'utf8');
 const values = await readFile(new URL('../client/values.js', import.meta.url), 'utf8');
 const { normalizeClientValue } = await import('data:text/javascript;base64,' + Buffer.from(values).toString('base64'));
 class Element {}
 class Input extends Element { constructor(properties) { super(); Object.assign(this, properties); } }
 const previousElement = globalThis.Element;
 globalThis.Element = Element;
 const previous = globalThis.HTMLInputElement;
 globalThis.HTMLInputElement = Input;
 try {
  const code = source.slice(source.indexOf('function formField('), source.indexOf('function normalizeActionArgs('));
  const bind = new Function('signatures','normalizeClientValue','FormData','err','isMultipleFileField','validFormFile',code+';return argsFromForm;')(
   { preview: ['name','age','active'] }, normalizeClientValue, class {}, (...args)=>args, ()=>false, ()=>true);
  const fields = { active: new Input({type:'checkbox',checked:false}), age: new Input({type:'number',valueAsNumber:42}), name: new Input({type:'text',value:'Ana'}) };
  const form = {elements:{namedItem:name=>fields[name]}};
  assert.deepEqual(bind('preview',form), [['Ana',42,false],null]);
  fields.active.checked=true;
  assert.deepEqual(bind('preview',form), [['Ana',42,true],null]);
  fields.age.valueAsNumber=NaN;
  assert.equal(bind('preview',form)[1][0],'INVALID_ARGUMENTS');
 } finally { globalThis.HTMLInputElement = previous; globalThis.Element = previousElement; }
});
