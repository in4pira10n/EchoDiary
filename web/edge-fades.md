# 页面边缘渐变

`static/edge-fades.js` 与 `static/edge-fades.css` 是独立组件，页面结构和主题由宿主提供。

```js
import { createEdgeFades } from './edge-fades.js';
const fades = createEdgeFades({
  container: document.querySelector('#app'),
  controls: () => document.querySelectorAll('[data-edge-fade-control]'),
  extension: 50,
  className: 'app-fades',
});
```

加载组件 CSS，并用 `.app-fades { --edge-fade-color: var(--app-background); }` 指定主题色。`--edge-fade-z` 默认为 10；页面内容应位于其下方，固定导航和操作按钮位于其上方。组件不拦截指针事件。

控件默认按实际位置自动归到顶部或底部，也可以通过 `data-edge-fade-control="top"` / `"bottom"` 指定方向。高度为控件向内最远边界距视口边缘的距离，再加 `extension`。没有可见控件的一侧保留 `extension` 高度。隐藏、inert 和 aria-hidden 的控件不参与测量。

组件响应控件尺寸、显隐、导航展开、窗口和 visualViewport 变化。动态增加全新的控件根节点后可调用 `fades.update()`；销毁页面时调用 `fades.destroy()`，同时清理观察器、监听器和渐变层。

EchoDiary 的 iOS PWA 偏移补偿保留在宿主 CSS 和 `viewport.js`，组件本身不包含平台和页面判断。
