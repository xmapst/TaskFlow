package utility

import (
	"context"
	"log/slog"

	xlog "golang.org/x/exp/slog"
)

// golang.org/x/exp/slog 是 slog 进标准库之前的一份快照。两个包的 Handler /
// Record / Attr / Value 同名同形，却是完全独立的两套类型：*xlog.Logger 不能
// 赋给 *slog.Logger，反之亦然。依赖 x/exp 的库（如 tusd）没法直接把项目的
// slog.Default() 传进去，只能在这里翻译一层。
//
// 桥接落在 Handler 上：实现 x/exp 的 Handler 接口，把每条 Record 逐字段翻译
// 成 log/slog 的 Record 再交给标准库 handler。上下文、级别、属性、分组原样
// 透传，只有类型需要转换。
//
// 消费方拿到的是 *xlog.Logger，写日志时仍走标准库的 handler，因此主题、级别
// 过滤、输出目标与项目其余部分完全一致。

// BridgeXExpLogger 把标准库的 handler 包成 x/exp/slog 的 logger。
func BridgeXExpLogger(handler slog.Handler) *xlog.Logger {
	return xlog.New(BridgeXExpHandler(handler))
}

// BridgeXExpHandler 把标准库的 handler 包成 x/exp/slog 的 handler，
// 供已经持有 *xlog.Logger 的场合替换其下游。
func BridgeXExpHandler(handler slog.Handler) xlog.Handler {
	return xexpBridge{inner: handler}
}

// xexpBridge 是 x/exp Handler 接口到标准库 Handler 的适配器。
type xexpBridge struct {
	inner slog.Handler
}

func (b xexpBridge) Enabled(ctx context.Context, level xlog.Level) bool {
	return b.inner.Enabled(ctx, slog.Level(level))
}

func (b xexpBridge) Handle(ctx context.Context, record xlog.Record) error {
	// PC 一起带过去，标准库据此还原调用点，日志才会指向真实位置，
	// 而不是 xexpBridge.Handle 这一行。
	out := slog.NewRecord(record.Time, slog.Level(record.Level), record.Message, record.PC)
	record.Attrs(func(attr xlog.Attr) bool {
		out.AddAttrs(b.bridgeAttr(attr))
		return true
	})
	return b.inner.Handle(ctx, out)
}

func (b xexpBridge) WithAttrs(attrs []xlog.Attr) xlog.Handler {
	converted := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		converted[i] = b.bridgeAttr(attr)
	}
	return xexpBridge{inner: b.inner.WithAttrs(converted)}
}

func (b xexpBridge) WithGroup(name string) xlog.Handler {
	return xexpBridge{inner: b.inner.WithGroup(name)}
}

func (b xexpBridge) bridgeAttr(attr xlog.Attr) slog.Attr {
	return slog.Attr{Key: attr.Key, Value: b.bridgeValue(attr.Value)}
}

// bridgeValue 按 Kind 逐个取值再重建。
// x/exp 的 Value 把小整数和字符串指针打包进 uintptr，只有 KindAny 的载荷是
// 真正的 any；其余种类直接丢给 slog.AnyValue 会被当成不可识别的值打出来。
func (b xexpBridge) bridgeValue(value xlog.Value) slog.Value {
	switch value.Kind() {
	case xlog.KindString:
		return slog.StringValue(value.String())
	case xlog.KindInt64:
		return slog.Int64Value(value.Int64())
	case xlog.KindUint64:
		return slog.Uint64Value(value.Uint64())
	case xlog.KindFloat64:
		return slog.Float64Value(value.Float64())
	case xlog.KindBool:
		return slog.BoolValue(value.Bool())
	case xlog.KindDuration:
		return slog.DurationValue(value.Duration())
	case xlog.KindTime:
		return slog.TimeValue(value.Time())
	case xlog.KindGroup:
		members := value.Group()
		attrs := make([]slog.Attr, len(members))
		for i, member := range members {
			attrs[i] = b.bridgeAttr(member)
		}
		return slog.GroupValue(attrs...)
	case xlog.KindLogValuer:
		// Resolve 保证返回值不再是 KindLogValuer，不会无限递归。
		return b.bridgeValue(value.Resolve())
	default:
		return slog.AnyValue(value.Any())
	}
}
