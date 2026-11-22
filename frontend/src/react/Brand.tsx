import mark from "./gikgok.svg?no-inline";
import s from "./App.module.css";

export const brandName = "GikGok";

export function Brand() {
  return (
    <>
      <img className={s.brandIcon} src={mark} alt="" width={40} height={40} />
      <span className={s.brandName}>{brandName}</span>
    </>
  );
}
