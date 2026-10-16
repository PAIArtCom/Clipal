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
