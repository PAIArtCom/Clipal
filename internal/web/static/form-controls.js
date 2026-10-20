// Shared numeric/unit editor. Configuration keeps its existing wire format;
// opening a form never rewrites saved durations or byte counts.
const durationScales = { ns: 0.000000001, us: 0.000001, 'µs': 0.000001, ms: 0.001, s: 1, m: 60, h: 3600 };
const sizeScales = { B: 1, KiB: 1024, MiB: 1048576, GiB: 1073741824 };

function unitFieldParts(raw, kind) {
    if (raw === '' || raw == null) return { value: '', unit: kind === 'bytes' ? 'MiB' : 's' };
    if (kind === 'bytes') {
        const bytes = Number(raw);
        const unit = ['GiB', 'MiB', 'KiB'].find(unit => bytes > 0 && bytes % sizeScales[unit] === 0) || 'B';
        return { value: String(bytes / sizeScales[unit]), unit };
    }
    const text = String(raw).trim();
    if (text === '0') return { value: '0', unit: 's' };
    const simple = text.match(/^([+]?(?:\d+(?:\.\d*)?|\.\d+))(ns|us|µs|ms|s|m|h)$/);
    if (simple) return { value: simple[1], unit: simple[2] };
    const parts = [...text.matchAll(/((?:\d+(?:\.\d*)?|\.\d+))(ns|us|µs|ms|s|m|h)/g)];
    if (parts.map(part => part[0]).join('') !== text || !parts.length) return { value: '', unit: 's', custom: text };
    const seconds = parts.reduce((total, part) => total + Number(part[1]) * durationScales[part[2]], 0);
    const unit = ['h', 'm', 's'].find(unit => seconds > 0 && Number.isInteger(seconds / durationScales[unit])) || 's';
    return { value: String(Number((seconds / durationScales[unit]).toPrecision(15))), unit };
}

function unitField(config) {
    let lastRaw = Symbol('initial');
    return {
        inputId: config.id,
        labelKey: config.label,
        kind: config.kind || 'duration',
        optional: !!config.optional,
        positive: false,
        disabled: false,
        value: '',
        unit: 's',
        custom: '',
        error: '',
        get choices() {
            const units = this.kind === 'bytes' ? ['B', 'KiB', 'MiB', 'GiB'] : ['ms', 's', 'm', 'h'];
            return units.includes(this.unit) ? units : [...units, this.unit];
        },
        mount(element) {
            element.appendChild(document.getElementById('unit-field-template').content.cloneNode(true));
        },
        sync() {
            this.disabled = config.disabled ? !!config.disabled() : false;
            this.positive = !!config.positive;
            const raw = config.get();
            if (raw === lastRaw) return;
            lastRaw = raw;
            const parts = unitFieldParts(raw, this.kind);
            this.value = parts.value;
            this.unit = parts.unit;
            this.custom = parts.custom || '';
            this.error = this.positive && parts.value !== '' && Number(parts.value) <= 0 ? 'forms.positiveDuration' : '';
        },
        commit(value) {
            this.value = value;
            this.custom = '';
            this.error = '';
            let raw = '';
            if (value !== '') {
                const number = Number(value);
                if (!Number.isFinite(number) || number < 0) this.error = 'forms.nonNegative';
                if (this.kind === 'bytes') {
                    raw = number * sizeScales[this.unit];
                    if (!Number.isSafeInteger(raw) || raw < 1) this.error = 'forms.wholeBytes';
                    else if (config.minimum && raw < config.minimum) this.error = 'forms.minimumSize';
                } else {
                    raw = `${value}${this.unit}`;
                    if (this.positive && number <= 0) this.error = 'forms.positiveDuration';
                    else if (config.minimum && number > 0 && number * durationScales[this.unit] < config.minimum) this.error = 'forms.minimumTimeout';
                }
            }
            lastRaw = raw;
            config.set(raw);
        }
    };
}

function choiceSearchText(value) {
    return String(value).normalize('NFD').replace(/\p{Diacritic}/gu, '')
        .toLowerCase().replace(/\bgmt(?=[+-])/g, 'utc')
        .replace(/([+-])0+(?=\d)/g, '$1').replace(/([+-]\d+):00\b/g, '$1');
}

// Search text is transient: only choosing an option changes the saved value.
function choicePicker(config) {
    let reposition;
    return {
        inputId: config.id,
        labelKey: config.label,
        open: false,
        query: '',
        active: -1,
        position: {},
        get disabled() { return config.disabled ? !!config.disabled() : false; },
        get choices() { return config.options(); },
        get selectedLabel() {
            return this.choices.find(option => option.value === config.get())?.label || config.get() || '';
        },
        get matches() {
            const terms = choiceSearchText(this.query).trim().split(/\s+/).filter(Boolean);
            return this.choices.filter(option => {
                const text = choiceSearchText(`${option.label} ${option.value} ${option.search || ''}`);
                const offset = choiceSearchText(option.label).match(/\butc([+-]\d+(?::\d{2})?)/)?.[1];
                return terms.every(term => /^(?:utc)?[+-]\d+(?::\d{2})?$/.test(term)
                    ? offset === term.replace(/^utc/, '') : text.includes(term));
            });
        },
        isSelected(value) { return value === config.get(); },
        mount(element) {
            element.appendChild(document.getElementById('choice-picker-template').content.cloneNode(true));
            reposition = () => { if (this.open) this.place(); };
            window.addEventListener('resize', reposition);
            document.addEventListener('scroll', reposition, true);
        },
        destroy() {
            window.removeEventListener('resize', reposition);
            document.removeEventListener('scroll', reposition, true);
        },
        place() {
            const rect = this.$refs.input.getBoundingClientRect();
            const below = window.innerHeight - rect.bottom - 8;
            const above = rect.top - 8;
            const flip = below < 240 && above > below;
            this.position = {
                left: `${Math.max(8, Math.min(rect.left, window.innerWidth - rect.width - 8))}px`,
                width: `${Math.min(rect.width, window.innerWidth - 16)}px`,
                maxHeight: `${Math.min(280, Math.max(80, flip ? above : below))}px`,
                top: flip ? 'auto' : `${rect.bottom + 4}px`,
                bottom: flip ? `${window.innerHeight - rect.top + 4}px` : 'auto'
            };
        },
        show() {
            if (this.disabled || this.open) return;
            this.query = '';
            this.open = true;
            this.active = Math.max(0, this.matches.findIndex(option => this.isSelected(option.value)));
            this.place();
            this.revealActive();
        },
        close() { this.open = false; this.query = ''; },
        search(value) {
            if (!this.open) this.show();
            this.query = value;
            this.active = this.matches.length ? 0 : -1;
            this.revealActive();
        },
        move(delta) {
            if (!this.open) { this.show(); return; }
            const count = this.matches.length;
            this.active = count ? (this.active + delta + count) % count : -1;
            this.revealActive();
        },
        edge(last) {
            if (!this.open) return;
            this.active = this.matches.length ? (last ? this.matches.length - 1 : 0) : -1;
            this.revealActive();
        },
        revealActive() {
            this.$nextTick(() => {
                this.$refs.list?.querySelector(`[data-choice-index="${this.active}"]`)
                    ?.scrollIntoView({ block: 'nearest' });
            });
        },
        choose(index) {
            const option = this.matches[index];
            if (!option || this.disabled) return;
            config.set(option.value);
            this.close();
            this.$nextTick(() => {
                if (document.activeElement === this.$refs.input) this.$refs.input?.select();
            });
        }
    };
}
