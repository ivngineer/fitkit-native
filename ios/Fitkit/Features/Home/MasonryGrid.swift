import SwiftUI

enum MasonryLayout {
    /// Distributes items into columns, always appending to the currently
    /// shortest column so heights stay balanced. Item order is preserved
    /// within each column. `aspectRatios` are width / height.
    static func columns(aspectRatios: [Double], columnCount: Int, spacing: Double = 0, captionHeight: Double = 0) -> [[Int]] {
        let count = max(1, columnCount)
        var columns = Array(repeating: [Int](), count: count)
        var heights = Array(repeating: 0.0, count: count)
        for (index, ratio) in aspectRatios.enumerated() {
            let safeRatio = ratio > 0 ? ratio : 1
            // Normalized to unit column width; clamp extreme panoramas and strips.
            let height = 1 / min(max(safeRatio, 0.3), 3) + captionHeight
            let shortest = heights.indices.min { heights[$0] < heights[$1] }!
            columns[shortest].append(index)
            heights[shortest] += height + spacing
        }
        return columns
    }

    static func columnCount(for width: CGFloat) -> Int {
        switch width {
        case ..<500: 2
        case ..<800: 3
        case ..<1100: 4
        default: 5
        }
    }
}

struct MasonryGrid<Content: View>: View {
    let pins: [Pin]
    let columnCount: Int
    var spacing: CGFloat = 10
    @ViewBuilder let content: (Pin) -> Content

    var body: some View {
        let columns = MasonryLayout.columns(
            aspectRatios: pins.map(\.aspectRatio),
            columnCount: columnCount,
            spacing: 0.04,
            captionHeight: 0.12
        )
        HStack(alignment: .top, spacing: spacing) {
            ForEach(columns.indices, id: \.self) { column in
                LazyVStack(spacing: spacing) {
                    ForEach(columns[column], id: \.self) { index in
                        content(pins[index])
                    }
                }
            }
        }
    }
}
