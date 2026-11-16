import { render } from "@testing-library/react";
import { describe, expect, test } from "vitest";
import { colourClass, Cover, shortName } from "./Cover";

const book = { id: 10, title: "The Secret Garden", authors: ["Frances Hodgson Burnett", "A. N. Other"] };

describe("Cover", () => {
  test("a book with a picture shows the picture", () => {
    const { container } = render(<Cover book={{ ...book, coverUrl: "/c.jpg" }} size="hero" />);
    const img = container.querySelector("img.cover.cover-hero.cover-img");
    expect(img).toHaveAttribute("src", "/c.jpg");
  });

  test("a book without one gets a generated cover in its own colour, with the title and every author", () => {
    const { container } = render(<Cover book={book} />);
    const cover = container.querySelector(".cover") as HTMLElement;
    expect(cover).toHaveClass(colourClass(10));
    expect(cover.querySelector(".ct")).toHaveTextContent("The Secret Garden");
    expect(cover.querySelector(".ca")).toHaveTextContent("Frances Hodgson Burnett, A. N. Other");
  });

  test("the card size names the first author by initials", () => {
    const { container } = render(<Cover book={book} size="card" />);
    expect(container.querySelector(".cover")).toHaveClass("cover-card");
    expect(container.querySelector(".ca")).toHaveTextContent("F. H. Burnett");
  });

  test("a book with no author shows no author line", () => {
    const { container } = render(<Cover book={{ ...book, authors: [] }} size="card" />);
    expect(container.querySelector(".ca")).toBeNull();
  });

  test("the palette repeats every eight books and never goes below c1", () => {
    expect(colourClass(0)).toBe("c1");
    expect(colourClass(8)).toBe("c1");
    expect(colourClass(-7)).toBe("c8");
  });

  test("shortName", () => {
    expect(shortName("Frances Hodgson Burnett")).toBe("F. H. Burnett");
    expect(shortName("Plato")).toBe("Plato");
  });
});
