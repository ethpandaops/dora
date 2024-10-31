import React, { CSSProperties } from 'react';
import { useAccount, useWriteContract } from 'wagmi';
import { useState } from 'react';
import { IValidator } from './SubmitConsolidationsFormProps';

interface IConsolidationReviewProps {
  sourceValidator: IValidator;
  targetValidator: IValidator;
  consolidationContract: string;
}

const ConsolidationReview = (props: IConsolidationReviewProps) => {
  
  return (
    <div>
      
    </div>
  );

};

export default ConsolidationReview;
