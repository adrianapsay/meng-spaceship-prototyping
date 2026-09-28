"""Parametric component library.

Every component uses the same local frame: the origin is the center of its
mounting face and the body extends along +Z. The agent places a component by
giving the position of that origin in the bus frame plus an XYZ Euler rotation.
"""

from __future__ import annotations

from typing import Annotated, ClassVar, Literal

from build123d import Align, Box, Cylinder, Location, Pos, Shape
from pydantic import BaseModel, ConfigDict, Field

Vec3 = Annotated[list[float], Field(min_length=3, max_length=3)]

BOTTOM = (Align.CENTER, Align.CENTER, Align.MIN)

# Areal density and cell performance for solar panels.
PANEL_KG_PER_M2 = 2.8
SOLAR_CONSTANT_W_PER_M2 = 1361.0
CELL_EFFICIENCY = 0.295
PACKING_FACTOR = 0.85


class Component(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: str = Field(
        pattern=r"^[a-z][a-z0-9_]{0,39}$",
        description="Unique snake_case id, e.g. 'solar_panel_px'.",
    )
    position_mm: Vec3 = Field(
        description="Bus-frame position [x, y, z] in mm of the component origin "
        "(center of its mounting face).",
    )
    rotation_deg: Vec3 = Field(
        default=[0.0, 0.0, 0.0],
        description="Intrinsic XYZ Euler rotation [rx, ry, rz] in degrees about the "
        "component origin. Local +Z is the direction the body extends.",
    )

    summary: ClassVar[str]
    color: ClassVar[str]
    deployable: ClassVar[bool] = False

    def local_shape(self) -> Shape:
        raise NotImplementedError

    def mass_kg(self) -> float:
        raise NotImplementedError

    def is_deployable(self) -> bool:
        return self.deployable

    def shape(self) -> Shape:
        return self.local_shape().moved(Location(tuple(self.position_mm), tuple(self.rotation_deg)))


class CameraPayload(Component):
    type: Literal["camera_payload"]
    aperture_mm: float = Field(50, ge=20, le=90, description="Lens aperture diameter.")
    length_mm: float = Field(100, ge=40, le=200, description="Overall optical length.")

    summary: ClassVar[str] = "Imaging telescope: square mounting base plus lens barrel along +Z."
    color: ClassVar[str] = "#2b2d42"

    def local_shape(self) -> Shape:
        base = self.aperture_mm + 20
        barrel = Cylinder(self.aperture_mm / 2 + 4, self.length_mm - 5, align=BOTTOM)
        return Box(base, base, 5, align=BOTTOM) + Pos(0, 0, 5) * barrel

    def mass_kg(self) -> float:
        return 0.15 + 0.45 * (self.aperture_mm / 50) ** 2 * (self.length_mm / 100)


class ReactionWheel(Component):
    type: Literal["reaction_wheel"]
    diameter_mm: float = Field(43, ge=25, le=60)
    height_mm: float = Field(18, ge=15, le=40)

    summary: ClassVar[str] = (
        "Single-axis reaction wheel; spin axis is local Z. Use 3 for 3-axis control."
    )
    color: ClassVar[str] = "#8d99ae"

    def local_shape(self) -> Shape:
        return Cylinder(self.diameter_mm / 2, self.height_mm, align=BOTTOM)

    def mass_kg(self) -> float:
        return 0.14 * (self.diameter_mm / 43) ** 2 * (self.height_mm / 18)


class StarTracker(Component):
    type: Literal["star_tracker"]
    baffle_length_mm: float = Field(60, ge=30, le=100)

    summary: ClassVar[str] = (
        "50x50x30 mm sensor head with a stray-light baffle along +Z (boresight)."
    )
    color: ClassVar[str] = "#5c677d"

    def local_shape(self) -> Shape:
        baffle = Cylinder(15, self.baffle_length_mm, align=BOTTOM)
        return Box(50, 50, 30, align=BOTTOM) + Pos(0, 0, 30) * baffle

    def mass_kg(self) -> float:
        return 0.2 + 0.001 * self.baffle_length_mm


class Battery(Component):
    type: Literal["battery"]
    capacity_wh: float = Field(40, ge=10, le=150)

    summary: ClassVar[str] = (
        "Li-ion pack, 90x90 mm footprint; height grows with capacity (10 + 0.4 mm/Wh)."
    )
    color: ClassVar[str] = "#e9c46a"

    def height_mm(self) -> float:
        return 10 + 0.4 * self.capacity_wh

    def local_shape(self) -> Shape:
        return Box(90, 90, self.height_mm(), align=BOTTOM)

    def mass_kg(self) -> float:
        return 0.05 + self.capacity_wh / 150


class ElectronicsStack(Component):
    type: Literal["electronics_stack"]
    boards: int = Field(3, ge=1, le=6, description="Number of 90x92 mm boards at 15 mm pitch.")

    summary: ClassVar[str] = (
        "PC/104-style OBC/EPS/radio board stack on corner standoffs; height = 15 mm per board."
    )
    color: ClassVar[str] = "#2a9d8f"

    def height_mm(self) -> float:
        return 15.0 * self.boards

    def local_shape(self) -> Shape:
        shape = Box(90, 92, 1.6, align=BOTTOM)
        for i in range(1, self.boards):
            shape += Pos(0, 0, 15 * i) * Box(90, 92, 1.6, align=BOTTOM)
        for x in (-40, 40):
            for y in (-41, 41):
                shape += Pos(x, y, 0) * Cylinder(2.5, self.height_mm(), align=BOTTOM)
        return shape

    def mass_kg(self) -> float:
        return 0.08 * self.boards


class PatchAntenna(Component):
    type: Literal["patch_antenna"]
    size_mm: float = Field(60, ge=30, le=90)

    summary: ClassVar[str] = (
        "Square flat patch antenna, 6 mm thick; radiates along +Z. Mount on an outer face."
    )
    color: ClassVar[str] = "#e76f51"

    def local_shape(self) -> Shape:
        return Box(self.size_mm, self.size_mm, 6, align=BOTTOM)

    def mass_kg(self) -> float:
        return 0.02 + 0.01 * (self.size_mm / 30) ** 2


class WhipAntenna(Component):
    type: Literal["whip_antenna"]
    length_mm: float = Field(300, ge=50, le=600)

    summary: ClassVar[str] = (
        "Deployed UHF/VHF tape antenna: 12x12x6 mm base plus 4 mm rod along +Z."
    )
    color: ClassVar[str] = "#f4a261"
    deployable: ClassVar[bool] = True

    def local_shape(self) -> Shape:
        rod = Cylinder(2, self.length_mm, align=BOTTOM)
        return Box(12, 12, 6, align=BOTTOM) + Pos(0, 0, 6) * rod

    def mass_kg(self) -> float:
        return 0.01 + 0.00005 * self.length_mm


class SolarPanel(Component):
    type: Literal["solar_panel"]
    width_mm: float = Field(ge=20, le=700, description="Panel extent along local X.")
    length_mm: float = Field(ge=20, le=700, description="Panel extent along local Y.")
    deployed: bool = Field(
        False,
        description="True for a deployed wing (exempt from the stowed envelope); "
        "false for a body-mounted panel.",
    )

    summary: ClassVar[str] = (
        "Flat solar array, width (X) x length (Y); cells face +Z. 2 mm body-mounted, 3 mm deployed."
    )
    color: ClassVar[str] = "#1d3557"

    def thickness_mm(self) -> float:
        return 3.0 if self.deployed else 2.0

    def area_m2(self) -> float:
        return self.width_mm * self.length_mm / 1e6

    def peak_power_w(self) -> float:
        return self.area_m2() * SOLAR_CONSTANT_W_PER_M2 * CELL_EFFICIENCY * PACKING_FACTOR

    def is_deployable(self) -> bool:
        return self.deployed

    def local_shape(self) -> Shape:
        return Box(self.width_mm, self.length_mm, self.thickness_mm(), align=BOTTOM)

    def mass_kg(self) -> float:
        return self.area_m2() * PANEL_KG_PER_M2 * (self.thickness_mm() / 2)


COMPONENT_TYPES: tuple[type[Component], ...] = (
    CameraPayload,
    ReactionWheel,
    StarTracker,
    Battery,
    ElectronicsStack,
    PatchAntenna,
    WhipAntenna,
    SolarPanel,
)
