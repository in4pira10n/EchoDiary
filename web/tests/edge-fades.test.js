// Uses synthetic geometry and a temporary fixture; never reads or edits notes.
async function runEdgeFadeTests() {
  const { edgeFadeHeights, createEdgeFades } = await import('/edge-fades.js?v=20261112-page-fades');
  const results = [];
  const assert = (condition, message) => { if (!condition) throw Error(message); };
  const test = (name, run) => { try { run(); results.push({name,passed:true}); } catch(error) { results.push({name,passed:false,error:String(error)}); } };
  for (const [name, height, top, bottom] of [['desktop',900,28,28],['tablet',1180,44,20],['compact phone',667,40,20],['tall phone',874,82,20]]) {
    test(`${name}: measured buttons plus edge gap plus 50`, () => {
      const result = edgeFadeHeights({top:0,bottom:height}, [{top,bottom:top+52}], [{top:height-bottom-52,bottom:height-bottom}]);
      assert(result.top===top+52+50 && result.bottom===bottom+52+50, 'Wrong fade height');
    });
  }
  test('Uses the largest actual button instead of a preset bar height', () => {
    const result = edgeFadeHeights({top:10,bottom:810}, [{bottom:90},{bottom:108}], [{top:700},{top:680}]);
    assert(result.top===148 && result.bottom===180, 'Ignored actual size or viewport position');
  });
  test('Edges without controls keep a light 50px fade', () => {
    const result = edgeFadeHeights({top:0,bottom:900}, [], []);
    assert(result.top===50 && result.bottom===50, 'Empty edges lost their light fade');
  });
  const fixture=document.createElement('div');
  fixture.style.cssText='position:fixed;left:-10000px;top:0;opacity:0;pointer-events:none';
  fixture.innerHTML='<header><button style="position:fixed;top:40px;height:60px">返回</button></header><footer><div class="echo-segmented" style="position:fixed;bottom:24px;height:56px"></div></footer>';
  document.body.append(fixture);
  const upper=fixture.querySelector('button'), lower=fixture.querySelector('.echo-segmented');
  const layout=createEdgeFades({ container:fixture, className:'diary-page-fades', controls:[upper,lower] });
  const layer=layout.element;
  try {
    test('Layer geometry tracks real DOM button measurements', () => {
      const canvas=layer.getBoundingClientRect(), upper=fixture.querySelector('button').getBoundingClientRect(), lower=fixture.querySelector('.echo-segmented').getBoundingClientRect();
      const expected=edgeFadeHeights(canvas,[upper],[lower]);
      assert(parseFloat(layer.style.getPropertyValue('--edge-fade-top'))===expected.top, 'Top did not use measured button');
      assert(parseFloat(layer.style.getPropertyValue('--edge-fade-bottom'))===expected.bottom, 'Bottom did not use measured button');
    });
    test('Controls automatically follow their actual edge after a layout change', () => {
      upper.style.top='auto';upper.style.bottom='90px';layout.update();
      assert(parseFloat(layer.style.getPropertyValue('--edge-fade-top'))===50, 'Bottom navigation created a top fade');
      assert(parseFloat(layer.style.getPropertyValue('--edge-fade-bottom'))===200, 'Relocated navigation was not measured');
      upper.style.top='40px';upper.style.bottom='auto';layout.update();
    });
    test('Hidden and closing navigation controls do not enlarge fades', () => {
      fixture.querySelector('header').hidden=true;lower.inert=true;layout.update();
      assert(parseFloat(layer.style.getPropertyValue('--edge-fade-top'))===50 && parseFloat(layer.style.getPropertyValue('--edge-fade-bottom'))===50, 'Hidden ancestor or inert navigation counted');
      fixture.querySelector('header').hidden=false;lower.inert=false;layout.update();
    });
    test('Fades do not intercept input and stay below writing controls', () => {
      assert(getComputedStyle(layer).pointerEvents==='none', 'Fade intercepts input');
      const z=Number(getComputedStyle(layer).zIndex);
      assert(z<Number(getComputedStyle(document.querySelector('#diary-header')).zIndex) && z<Number(getComputedStyle(document.querySelector('#add-action')).zIndex) && z<Number(getComputedStyle(document.querySelector('#writing-top')).zIndex) && z<Number(getComputedStyle(document.querySelector('#writing-actions')).zIndex), 'Fade covers controls');
    });
    test('Both ends use the current theme background with transparent stops', () => {
      const original=document.documentElement.dataset.theme;
      try {
        for (const theme of ['dark','light']) {
          document.documentElement.dataset.theme=theme;
          const backgrounds=['::before','::after'].map(p=>getComputedStyle(layer,p).backgroundImage);
          assert(backgrounds.every(value=>value.includes('linear-gradient') && value.includes('0)')), 'Missing transparent gradient');
          if(theme==='dark') assert(backgrounds.every(value=>value.includes('0, 0, 0')), 'Dark fade is not black');
        }
      } finally { document.documentElement.dataset.theme=original; }
    });
  } finally { layout.destroy();fixture.remove(); }
  return {passed:results.filter(r=>r.passed).length,failed:results.filter(r=>!r.passed).length,results};
}
