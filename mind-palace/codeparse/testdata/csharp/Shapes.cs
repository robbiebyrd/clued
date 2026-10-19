// Shapes: every kind of definition C# has, in a block namespace.
using System;
using System.Collections.Generic;

namespace Demo.Geometry
{
    /// <summary>
    /// A shape that knows its area.
    /// </summary>
    public interface IShape
    {
        /// <summary>The area in square units.</summary>
        double Area();

        string Name { get; }

        event EventHandler Changed;
    }

    /// <summary>The colours a shape can be drawn in.</summary>
    public enum Color
    {
        Red,
        Green = 2,
        Blue,
    }

    /// <summary>A point on the plane.</summary>
    public readonly struct Point
    {
        public readonly double X;
        public readonly double Y;

        public Point(double x, double y)
        {
            X = x;
            Y = y;
        }
    }

    /// <summary>A size, positional.</summary>
    public record Size(double Width, double Height);

    public record struct Margin(int Top, int Bottom);

    internal delegate void ShapeHandler(IShape shape);

    /// <summary>A circle around a centre.</summary>
    [Serializable]
    public class Circle : IShape
    {
        public const int Sides = 0;
        private static readonly string kind = "circle", plural = "circles";
        private double radius;

        public event EventHandler Changed;

        public Circle(double radius)
        {
            this.radius = radius;
        }

        ~Circle()
        {
        }

        /// <summary>The radius, never negative.</summary>
        public double Radius
        {
            get => radius;
            set => radius = Math.Max(0, value);
        }

        public string Name => kind;

        public double this[int index] => index == 0 ? radius : 0;

        public double Area() => Math.PI * radius * radius;

        protected virtual void OnChanged() => Changed?.Invoke(this, EventArgs.Empty);

        public static Circle operator *(Circle c, double k) => new Circle(c.radius * k);

        public static implicit operator double(Circle c) => c.radius;

        private int Compare(Circle other) => radius.CompareTo(other.radius);

        /// <summary>Builds circles.</summary>
        public sealed class Builder
        {
            private readonly List<double> radii = new List<double>();

            public Builder Add(double r)
            {
                radii.Add(r);
                return this;
            }
        }
    }

    public abstract class Shape
    {
        public abstract double Area();

        protected internal string Label { get; init; } = "";
    }
}
