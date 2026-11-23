import AppKit

/// Direction B ("Orbital Control") menu bar mark, drawn directly from the
/// 32x32 vector geometry so the menu needs no resource bundle.
public enum BrandMark {
    /// Returns a square template image at `size` points (18 pt for the macOS
    /// menu bar). Template images use only their alpha channel, so macOS tints
    /// the mark for light, dark and translucent bars.
    public static func image(size: CGFloat = 18) -> NSImage {
        let image = NSImage(size: NSSize(width: size, height: size), flipped: false) { rect in
            let scale = min(rect.width, rect.height) / 32
            func point(_ x: CGFloat, _ y: CGFloat) -> NSPoint {
                NSPoint(x: rect.minX + x * scale, y: rect.minY + (32 - y) * scale)
            }

            // Opaque mask only: AppKit discards colour channels on template images.
            NSColor.black.set()

            // Open ring arc: M24.2 10.6 A10 10 0 1 0 26 16 (viewBox 32, y flipped).
            let arc = NSBezierPath()
            arc.appendArc(
                withCenter: point(16.005496, 16.331501),
                radius: 10 * scale,
                startAngle: 34.9702,
                endAngle: 1.8997,
                clockwise: false
            )
            arc.lineWidth = 2.4 * scale
            arc.lineCapStyle = .square
            arc.stroke()

            // Inward return arrowhead: M25.6 6.2 V11.2 H20.6.
            let head = NSBezierPath()
            head.move(to: point(25.6, 6.2))
            head.line(to: point(25.6, 11.2))
            head.line(to: point(20.6, 11.2))
            head.lineWidth = 2.2 * scale
            head.lineCapStyle = .square
            head.lineJoinStyle = .miter
            head.stroke()

            // Central gate bar: rect x=11 y=14.5 width=10 height=3.
            let barOrigin = point(11, 14.5 + 3)
            let bar = NSRect(x: barOrigin.x, y: barOrigin.y, width: 10 * scale, height: 3 * scale)
            NSBezierPath(rect: bar).fill()

            return true
        }
        image.isTemplate = true
        return image
    }
}
