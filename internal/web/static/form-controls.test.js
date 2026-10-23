const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function sliderFixture() {
    let value = 'first';
    let active = 0;
    let shown = true;
    let nextFrame = 0;
    const ticks = [];
    const frames = new Map();
    const attributes = new Set();
    const observers = [];
    const listeners = new Map();
    const items = [0, 1, 2].map((index) => ({
        offsetLeft: [2, 82, 202][index], offsetTop: 2,
        offsetWidth: [80, 120, 100][index], offsetHeight: 28,
        matches: () => true,
        classList: { contains: () => index === active }
    }));
    const root = {
        children: [...items],
        classList: { add() {}, remove() {} },
        appendChild(element) { this.children.push(element); },
        setAttribute(name) { attributes.add(name); },
        removeAttribute(name) { attributes.delete(name); },
        toggleAttribute(name, on) { if (on) attributes.add(name); else attributes.delete(name); },
        getClientRects: () => shown ? [{}] : []
    };
    class Observer {
        constructor(callback) { this.callback = callback; this.disconnected = false; observers.push(this); }
        observe() { this.disconnected = false; }
        disconnect() { this.disconnected = true; }
    }
    const context = {
        document: { createElement: () => ({ style: {}, setAttribute() {}, matches: () => false, remove() {} }) },
        window: {
            addEventListener(name, callback) { listeners.set(name, callback); },
            removeEventListener(name) { listeners.delete(name); }
        },
        ResizeObserver: Observer, MutationObserver: Observer,
        requestAnimationFrame(callback) { frames.set(++nextFrame, callback); return nextFrame; },
        cancelAnimationFrame(id) { frames.delete(id); }
    };
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'form-controls.js'), 'utf8'), context);
    const slider = context.slidingSelection(() => value);
    slider.$nextTick = callback => ticks.push(callback);
    slider.mount(root);
    const flush = () => {
        while (ticks.length) ticks.shift()();
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach(callback => callback());
    };
    return {
        slider, root, items, attributes, observers, frames, listeners, flush,
        indicator: root.children.at(-1),
        commit(next, index) { value = next; active = index; slider.sync(); },
        show(next) { shown = next; observers[0].callback(); }
    };
}

test('slider snaps on first layout and animates only a committed selection', () => {
    const f = sliderFixture();
    f.flush();
    assert.ok(f.attributes.has('data-selection-ready'));
    assert.ok(!f.attributes.has('data-selection-animate'));
    assert.equal(f.indicator.style.transform, 'translate3d(2px, 2px, 0)');
    // No state commit means a cancelled or failed operation cannot move it.
    f.slider.sync();
    f.flush();
    assert.equal(f.indicator.style.transform, 'translate3d(2px, 2px, 0)');
    f.commit('second', 1);
    f.flush();
    assert.ok(f.attributes.has('data-selection-animate'));
    assert.equal(f.indicator.style.transform, 'translate3d(82px, 2px, 0)');
    assert.equal(f.indicator.style.width, '120px');
    f.slider.sync();
    f.observers[0].callback();
    f.flush();
    assert.ok(f.attributes.has('data-selection-animate'), 'unchanged observer notifications preserve the running transition');
});

test('rapid selection changes share one frame and target the latest state', () => {
    const f = sliderFixture();
    f.flush();
    f.commit('second', 1);
    f.commit('third', 2);
    f.flush();
    assert.equal(f.indicator.style.transform, 'translate3d(202px, 2px, 0)');
    assert.ok(f.attributes.has('data-selection-animate'));
    f.commit('first', 0);
    f.flush();
    assert.equal(f.indicator.style.transform, 'translate3d(2px, 2px, 0)');
});

test('resizing and revealing hidden controls recalibrates without animation', () => {
    const f = sliderFixture();
    f.flush();
    f.commit('second', 1);
    f.flush();
    f.items[1].offsetWidth = 150;
    f.observers[0].callback();
    f.flush();
    assert.equal(f.indicator.style.width, '150px');
    assert.ok(!f.attributes.has('data-selection-animate'));
    f.show(false);
    f.flush();
    assert.ok(!f.attributes.has('data-selection-ready'));
    f.commit('third', 2);
    f.flush();
    f.show(true);
    f.flush();
    assert.ok(f.attributes.has('data-selection-ready'));
    assert.ok(!f.attributes.has('data-selection-animate'));
    assert.equal(f.indicator.style.transform, 'translate3d(202px, 2px, 0)');
});

test('dynamic option layout snaps and destruction releases observers and pending frames', () => {
    const f = sliderFixture();
    f.flush();
    f.items[0].offsetWidth = 160;
    f.observers[1].callback();
    f.flush();
    assert.equal(f.indicator.style.width, '160px');
    assert.ok(!f.attributes.has('data-selection-animate'));
    f.commit('second', 1);
    f.slider.destroy();
    f.flush();
    assert.equal(f.frames.size, 0);
    assert.equal(f.listeners.size, 0);
    assert.ok(f.observers.every(observer => observer.disconnected));
    assert.ok(!f.attributes.has('data-selection-ready'));
});

test('visibility-only dialogs snap on reopening even though their layout stays measurable', () => {
    const f = sliderFixture();
    f.slider.sync(false);
    f.flush();
    assert.ok(!f.attributes.has('data-selection-ready'));
    f.slider.sync(true);
    f.flush();
    f.commit('second', 1);
    f.flush();
    assert.ok(f.attributes.has('data-selection-animate'));
    f.slider.sync(false);
    f.flush();
    assert.ok(!f.attributes.has('data-selection-ready'));
    f.commit('first', 0);
    f.slider.sync(true);
    f.flush();
    assert.ok(f.attributes.has('data-selection-ready'));
    assert.ok(!f.attributes.has('data-selection-animate'));
});
